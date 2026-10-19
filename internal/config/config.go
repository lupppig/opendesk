// Package config loads opendesk configuration from the environment.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MinAuthSecretLength is the hard floor for AUTH_SECRET; the realtime gateway enforces the same.
const MinAuthSecretLength = 32

type Config struct {
	Env      string // development | production
	Role     string // all | api | worker
	HTTPAddr string
	DataDir  string

	// Public URLs. Self-hosted single domain: APIURL == AppURL, GatewayURL = wss://host/socket.
	APIURL     string
	AppURL     string
	GatewayURL string
	SiteURL    string
	ServeWeb   bool // serve the embedded web app and /config.js from this binary
	// AdminURL is where the instance admin panel lives (ADMIN_URL, default <APP_URL>/admin,
	// or the panel's dev server on a development API that does not serve the web app);
	// its origin is the only one the admin routes accept (contracts/admin.md).
	AdminURL string

	// DatabaseURL is the pooled connection (PgBouncer in transaction mode) used by request handlers.
	// DatabaseURLDirect is unpooled; migrations, River (LISTEN/NOTIFY) and the event relay use it.
	DatabaseURL       string
	DatabaseURLDirect string

	AuthSecret     []byte
	CredentialsKey []byte // 32 bytes, AES-256-GCM for mailbox credentials and webhook secrets
	CookieDomain   string
	CookieSecure   bool
	CORSOrigins    []string
	SignupEnabled  bool
	TrustProxy     bool
	ProxyHops      int // TRUSTED_PROXY_HOPS: proxies in front that append to X-Forwarded-For
	// LogLevel is the least severe level logged (LOG_LEVEL): info in production,
	// debug in development.
	LogLevel slog.Level
	// UpdateCheck looks for new opendesk releases on GitHub (UPDATE_CHECK, default on) and
	// tells the instance admins. Builds that are not a release never look.
	UpdateCheck bool

	NATSURL      string // empty = run NATS embedded in this process
	NATSToken    string
	NATSListen   string // embedded server listen address, e.g. 127.0.0.1:4222
	NATSStoreDir string

	StorageDriver    string // local | s3
	S3Bucket         string
	S3Region         string
	S3Endpoint       string // R2 / MinIO
	S3AccessKey      string
	S3SecretKey      string
	S3ForcePathStyle bool

	MailDriver   string // log | smtp | ses
	MailFrom     string // "opendesk <team@opendesk.sh>"
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPTLS      string // tls | starttls | none
	SESRegion    string

	InboundDomain   string // forwarding addresses live here, e.g. in.opendesk.sh
	InboundSecret   string // shared secret for POST /v1/inbound/email
	InboundSMTPAddr string // optional built-in SMTP receiver, e.g. :2525
	// InboundAuthservIDs are the authserv-ids whose Authentication-Results on inbound mail
	// are believed (INBOUND_AUTHSERV_ID, comma-separated), e.g. mx.cloudflare.net.
	InboundAuthservIDs []string

	GoogleClientID     string
	GoogleClientSecret string
	// GoogleMailboxClientID and GoogleMailboxClientSecret are the Google client mailbox
	// connections use (GOOGLE_MAILBOX_CLIENT_ID, GOOGLE_MAILBOX_CLIENT_SECRET), when it is
	// not the sign-in client; the sign-in client otherwise.
	GoogleMailboxClientID     string
	GoogleMailboxClientSecret string
	MicrosoftClientID         string
	MicrosoftClientSecret     string
	MicrosoftTenant           string
	// MicrosoftMailboxClientID and MicrosoftMailboxClientSecret are the Microsoft app
	// mailbox connections use (MICROSOFT_MAILBOX_CLIENT_ID, MICROSOFT_MAILBOX_CLIENT_SECRET),
	// when it is not the sign-in app; the sign-in app otherwise.
	MicrosoftMailboxClientID     string
	MicrosoftMailboxClientSecret string
	GitHubClientID               string
	GitHubClientSecret           string
	OIDCIssuer                   string
	OIDCClientID                 string
	OIDCClientSecret             string
	OIDCName                     string
	// GoogleMailboxOAuth and MicrosoftMailboxOAuth allow connecting a new mailbox with
	// that provider's sign-in (GOOGLE_MAILBOX_OAUTH, MICROSOFT_MAILBOX_OAUTH). Each is on
	// when the provider's client is configured, unless set to false. Reconnecting a
	// mailbox that already uses the provider, and signing in to opendesk with it, are
	// not affected.
	GoogleMailboxOAuth    bool
	MicrosoftMailboxOAuth bool
	// MailhostISPDB lets mailbox server detection ask Thunderbird's ISPDB
	// (autoconfig.thunderbird.net), which sends it the domain name. Default: on when
	// billing is configured (the hosted service), off otherwise.
	MailhostISPDB bool
	// MailboxIMAP lets mailboxes receive over IMAP (MAILBOX_IMAP, default on): generic
	// IMAP, and Google and Microsoft mailboxes connected to read the inbox. Off, every
	// mailbox receives by forwarding and Google and Microsoft connect send-only; existing
	// IMAP mailboxes are kept but not polled. opendesk Cloud turns it off. Workspaces with
	// Collab still receive over generic IMAP (with COLLAB_IMAP) and may read Google and
	// Microsoft inboxes through their APIs with the Collab clients (contracts/api.md).
	MailboxIMAP bool
	// MailboxSystemSend lets mailboxes send replies through the platform mailer
	// (MAILBOX_SYSTEM_SEND, default on): outbound_kind "system", from MAIL_FROM. Off,
	// every mailbox sends through its own Google, Microsoft or SMTP server, and one that
	// sends through the platform mailer sends nothing until it connects one. opendesk
	// Cloud turns it off.
	MailboxSystemSend bool
	// Collab (contracts/collab.md): members' own mail accounts. COLLAB (default on) makes it
	// exist on this instance; with billing it is still only the collab plan's. The OAuth
	// clients default to the mailbox clients; COLLAB_IMAP (default on) offers IMAP accounts.
	Collab                      bool
	CollabIMAP                  bool
	CollabGoogleClientID        string
	CollabGoogleClientSecret    string
	CollabMicrosoftClientID     string
	CollabMicrosoftClientSecret string
	// StripePriceCollab and StripePriceCollabYear are the collab plan's recurring per-seat
	// prices (contracts/billing.md "Collab"); without one the plan cannot be bought.
	StripePriceCollab     string
	StripePriceCollabYear string

	TurnstileSiteKey string
	TurnstileSecret  string
	// Native apps (contracts/native.md). AppAttestAppIDs ("TEAMID.bundle") turn App Attest
	// proofs on; AppAttestDevelopment also accepts development-environment keys.
	AppAttestAppIDs      []string
	AppAttestDevelopment bool
	// APNs token auth. With a key this instance pushes to iPhones itself (push = direct).
	APNsKey    string // the .p8 key, PEM or base64
	APNsKeyID  string
	APNsTeamID string
	APNsTopic  string
	// PushRelayURL is the relay origin used without an APNs key (push = relay).
	PushRelayURL string
	// PushRelayServe serves /v1/push/relay/* for other instances (needs an APNs key).
	PushRelayServe bool

	// Help center addresses (contracts/teams.md "Help center addresses"). HelpDomain puts
	// each brand's help center at <slug>.<HelpDomain> (HELP_DOMAIN, e.g. opendeskhelp.com);
	// without it a help center is at HelpPathBase()/help/<slug>. HelpCNAMETarget is what custom
	// domains point at. HelpProxySecret is shared with the edge proxy in front of help
	// centers, which names the host it was asked for. CloudflareSaaS* add custom domains to
	// a Cloudflare for SaaS zone; without them the instance's own proxy issues certificates.
	HelpDomain           string
	HelpCNAMETarget      string
	HelpProxySecret      string
	CloudflareSaaSZoneID string
	CloudflareSaaSToken  string

	// LogoDevToken turns on logo.dev as the first source of company logos (sends the
	// company's domain to logo.dev).
	LogoDevToken string

	PosthogKey  string
	PosthogHost string

	EventRetention time.Duration

	// Billing (opendesk Cloud). Billing is on only when STRIPE_SECRET_KEY is set; without it
	// every workspace has unlimited use, which is the self-hosted default.
	StripeSecretKey     string
	StripeWebhookSecret string
	StripePricePro      string // recurring per-seat price, monthly
	StripePriceProYear  string // optional recurring per-seat price, yearly
	StripePriceTopUp    string // one-time price for an AI usage top-up ($10 of usage each)
	StripeAPIURL        string // development only: a local Stripe fake or stripe-mock instead of api.stripe.com

	// AI. AIProvider: openrouter | anthropic | openai (any OpenAI-compatible endpoint, e.g. Ollama) | none.
	AIProvider        string
	AnthropicAPIKey   string
	OpenAIAPIKey      string
	OpenAIBaseURL     string
	OpenRouterAPIKey  string
	OpenRouterBaseURL string // API root; chat is {base}/v1/chat/completions, Jev {base}/alpha/decisions
	AIModel           string // drafting, summaries, answers
	AIFastModel       string // triage, classification, short rewrites
	// AIModels are the models a workspace may choose (contracts/ai.md "Models"); empty = the defaults.
	AIModels         []string
	EmbeddingsDriver string // openrouter | voyage | openai | none
	VoyageAPIKey     string
	EmbeddingsModel  string
	// AIUsageMarkup multiplies a call's provider cost into what the workspace pays when
	// billing is on (contracts/billing.md "Metered usage").
	AIUsageMarkup float64

	// Decisions (contracts/ai.md "Decisions"). DecisionsProvider: typesafe (Jev) | openrouter (Jev) | llm | none.
	DecisionsProvider string
	TypesafeAPIKey    string
	TypesafeAPIURL    string // base URL; the API path is /v1/systemone
	TypesafeModel     string
	// NotifyEmail paces notification emails (contracts/api.md "Notification preferences and emails").
	NotifyEmail NotifyEmail
	// EmailLegal identifies the company that sends platform email (EMAIL_LEGAL_*). It has no
	// default: an instance shows a company only when its operator sets one.
	EmailLegal EmailLegal
}

// EmailLegal is the sender's company identification for the footer of platform email.
type EmailLegal struct {
	Entity, Number, Place, Address string
}

// Line is the footer line, such as "Acme Ltd, registered in England and Wales, company
// number 123. Registered office: 1 High Street, London." Empty without an entity.
func (l EmailLegal) Line() string {
	if l.Entity == "" {
		return ""
	}
	out := l.Entity
	switch {
	case l.Place != "" && l.Number != "":
		out += ", registered in " + l.Place + ", company number " + l.Number
	case l.Number != "":
		out += ", company number " + l.Number
	case l.Place != "":
		out += ", registered in " + l.Place
	}
	out += "."
	if l.Address != "" {
		out += " Registered office: " + l.Address + "."
	}
	return out
}

// DecisionsEnabled reports whether a decision engine is configured.
func (c *Config) DecisionsEnabled() bool {
	return c.DecisionsProvider == "typesafe" || c.DecisionsProvider == "openrouter" || (c.DecisionsProvider == "llm" && c.AIEnabled())
}

// PushMode is how this instance reaches iPhones: direct (APNs key), relay or off.
func (c *Config) PushMode() string {
	switch {
	case c.APNsKey != "":
		return "direct"
	case c.PushRelayURL != "":
		return "relay"
	}
	return "off"
}

// MailboxOAuthProviders lists the sign-ins a new mailbox may connect with, for the
// web app's runtime config (mailboxOAuth).
func (c *Config) MailboxOAuthProviders() []string {
	out := []string{}
	if c.GoogleMailboxOAuth {
		out = append(out, "google")
	}
	if c.MicrosoftMailboxOAuth {
		out = append(out, "microsoft")
	}
	return out
}

// HelpCenterURL is a brand's default help center address: https://<slug>.<HELP_DOMAIN>
// (the scheme of APP_URL), else HelpPathBase()/help/<slug>.
func (c *Config) HelpCenterURL(slug string) string {
	if c.HelpDomain != "" {
		scheme := "https"
		if strings.HasPrefix(c.AppURL, "http://") {
			scheme = "http"
		}
		return scheme + "://" + slug + "." + c.HelpDomain
	}
	return c.HelpPathBase() + "/help/" + slug
}

// HelpPathBase is where /help/<slug> is served without HELP_DOMAIN: APP_URL when this
// binary serves the web app, else API_URL.
func (c *Config) HelpPathBase() string {
	if c.ServeWeb || c.APIURL == "" {
		return strings.TrimRight(c.AppURL, "/")
	}
	return strings.TrimRight(c.APIURL, "/")
}

// CloudflareSaaS reports whether custom help center domains are Cloudflare custom hostnames.
func (c *Config) CloudflareSaaS() bool {
	return c.CloudflareSaaSZoneID != "" && c.CloudflareSaaSToken != ""
}

// AdminOrigin is the scheme and host of AdminURL: the origin the admin panel runs on.
func (c *Config) AdminOrigin() string {
	u, err := url.Parse(c.AdminURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// BillingEnabled reports whether plans, limits and the AI balance are enforced.
func (c *Config) BillingEnabled() bool { return c.StripeSecretKey != "" }

// AIEnabled reports whether an AI provider is configured.
func (c *Config) AIEnabled() bool { return c.AIProvider != "none" && c.AIProvider != "" }

func (c *Config) IsProduction() bool { return c.Env == "production" }

// NotifyEmail paces notification emails per person and per instance.
type NotifyEmail struct {
	// Pacing names the defaults: strict (opendesk Cloud: billing on) or relaxed.
	Pacing string
	// Delay: the first email after a quiet spell waits this long for the notification to be read.
	Delay time.Duration
	// Cooldowns: the gap after each email while activity continues; the last one repeats.
	Cooldowns []time.Duration
	// QuietReset: with nothing new for this long, the next email starts over at the first step.
	QuietReset time.Duration
	// UrgentGap: urgent notifications may cut a cooldown short, never sooner than this after the last email.
	UrgentGap time.Duration
	// UrgentPerDay caps the emails sent early for urgent notifications, per person.
	UrgentPerDay int
	// PerHour and PerDay cap notification emails per person (rolling windows).
	PerHour, PerDay int
	// InstancePerMinute caps notification emails across the instance; more wait, none are dropped.
	InstancePerMinute int
	// ActiveWindow: emails wait while the person was active in the app this recently.
	ActiveWindow time.Duration
}

// NotifyEmailDefaults are the pacing defaults: strict for opendesk Cloud, relaxed for
// self-hosted instances.
func NotifyEmailDefaults(strict bool) NotifyEmail {
	if strict {
		return NotifyEmail{Pacing: "strict", Delay: 5 * time.Minute, Cooldowns: []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour},
			QuietReset: 2 * time.Hour, UrgentGap: 10 * time.Minute, UrgentPerDay: 6, PerHour: 4, PerDay: 24, InstancePerMinute: 120,
			ActiveWindow: 5 * time.Minute}
	}
	return NotifyEmail{Pacing: "relaxed", Delay: 5 * time.Minute, Cooldowns: []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour},
		QuietReset: 2 * time.Hour, UrgentGap: 5 * time.Minute, UrgentPerDay: 12, PerHour: 12, PerDay: 100, InstancePerMinute: 60,
		ActiveWindow: 5 * time.Minute}
}

// loadNotifyEmail reads NOTIFY_EMAIL_*: NOTIFY_EMAIL_PACING picks the defaults (strict when
// billing is on), and each other variable overrides one value.
func loadNotifyEmail(billing bool) (NotifyEmail, error) {
	pacing := env("NOTIFY_EMAIL_PACING", map[bool]string{true: "strict", false: "relaxed"}[billing])
	if pacing != "strict" && pacing != "relaxed" {
		return NotifyEmail{}, fmt.Errorf("NOTIFY_EMAIL_PACING must be strict or relaxed, got %q", pacing)
	}
	n := NotifyEmailDefaults(pacing == "strict")
	n.Delay = envDuration("NOTIFY_EMAIL_DELAY", n.Delay)
	if v := envList("NOTIFY_EMAIL_COOLDOWNS"); len(v) > 0 {
		n.Cooldowns = nil
		for _, s := range v {
			d, err := time.ParseDuration(s)
			if err != nil || d <= 0 {
				return NotifyEmail{}, fmt.Errorf("NOTIFY_EMAIL_COOLDOWNS must be durations such as 15m,30m,1h, got %q", s)
			}
			n.Cooldowns = append(n.Cooldowns, d)
		}
	}
	n.QuietReset = envDuration("NOTIFY_EMAIL_QUIET_RESET", n.QuietReset)
	n.UrgentGap = envDuration("NOTIFY_EMAIL_URGENT_GAP", n.UrgentGap)
	n.UrgentPerDay = envInt("NOTIFY_EMAIL_URGENT_PER_DAY", n.UrgentPerDay)
	n.PerHour = envInt("NOTIFY_EMAIL_PER_HOUR", n.PerHour)
	n.PerDay = envInt("NOTIFY_EMAIL_PER_DAY", n.PerDay)
	n.InstancePerMinute = envInt("NOTIFY_EMAIL_INSTANCE_PER_MINUTE", n.InstancePerMinute)
	n.ActiveWindow = envDuration("NOTIFY_EMAIL_ACTIVE_WINDOW", n.ActiveWindow)
	return n, n.Validate()
}

// Validate refuses pacing that would email without limits or never.
func (n NotifyEmail) Validate() error {
	longest := time.Duration(0)
	for _, d := range n.Cooldowns {
		if d <= 0 || d > 24*time.Hour {
			return errors.New("NOTIFY_EMAIL_COOLDOWNS must be between 1s and 24h each")
		}
		longest = max(longest, d)
	}
	switch {
	case len(n.Cooldowns) == 0 || len(n.Cooldowns) > 10:
		return errors.New("NOTIFY_EMAIL_COOLDOWNS needs 1 to 10 steps")
	case n.Delay < 0 || n.Delay > 24*time.Hour:
		return errors.New("NOTIFY_EMAIL_DELAY must be between 0 and 24h")
	case n.QuietReset < longest:
		return errors.New("NOTIFY_EMAIL_QUIET_RESET must be at least the longest cooldown")
	case n.UrgentGap < 0 || n.UrgentPerDay < 0:
		return errors.New("NOTIFY_EMAIL_URGENT_GAP and NOTIFY_EMAIL_URGENT_PER_DAY must not be negative")
	case n.PerHour < 1 || n.PerDay < n.PerHour:
		return errors.New("NOTIFY_EMAIL_PER_HOUR must be at least 1 and NOTIFY_EMAIL_PER_DAY at least NOTIFY_EMAIL_PER_HOUR")
	case n.InstancePerMinute < 1:
		return errors.New("NOTIFY_EMAIL_INSTANCE_PER_MINUTE must be at least 1")
	case n.ActiveWindow < 0 || n.ActiveWindow > 24*time.Hour:
		return errors.New("NOTIFY_EMAIL_ACTIVE_WINDOW must be between 0 and 24h")
	}
	return nil
}

// Load reads the environment. It refuses to boot on unsafe secrets.
func Load() (*Config, error) {
	c := &Config{
		Env:                env("OPENDESK_ENV", "development"),
		Role:               env("OPENDESK_ROLE", "all"),
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		DataDir:            env("DATA_DIR", "./data"),
		APIURL:             strings.TrimRight(env("API_URL", "http://localhost:8080"), "/"),
		AppURL:             strings.TrimRight(env("APP_URL", "http://localhost:5173"), "/"),
		GatewayURL:         strings.TrimRight(env("GATEWAY_URL", "ws://localhost:4000/socket"), "/"),
		SiteURL:            strings.TrimRight(env("SITE_URL", "https://opendesk.sh"), "/"),
		ServeWeb:           envBool("SERVE_WEB", false),
		DatabaseURL:        env("DATABASE_URL", "postgres://opendesk:opendesk@localhost:5432/opendesk?sslmode=disable"),
		DatabaseURLDirect:  env("DATABASE_URL_DIRECT", ""),
		CookieDomain:       env("COOKIE_DOMAIN", ""),
		SignupEnabled:      envBool("SIGNUP_ENABLED", true),
		TrustProxy:         envBool("TRUST_PROXY", false),
		UpdateCheck:        envBool("UPDATE_CHECK", true),
		NATSURL:            env("NATS_URL", ""),
		NATSToken:          env("NATS_TOKEN", ""),
		NATSListen:         env("NATS_LISTEN", "127.0.0.1:4222"),
		StorageDriver:      env("STORAGE_DRIVER", "local"),
		S3Bucket:           env("S3_BUCKET", ""),
		S3Region:           env("S3_REGION", "auto"),
		S3Endpoint:         env("S3_ENDPOINT", ""),
		S3AccessKey:        env("S3_ACCESS_KEY_ID", ""),
		S3SecretKey:        env("S3_SECRET_ACCESS_KEY", ""),
		S3ForcePathStyle:   envBool("S3_FORCE_PATH_STYLE", false),
		MailDriver:         env("MAIL_DRIVER", "log"),
		MailFrom:           env("MAIL_FROM", "opendesk <notifications@localhost>"),
		SMTPHost:           env("SMTP_HOST", ""),
		SMTPPort:           envInt("SMTP_PORT", 587),
		SMTPUsername:       env("SMTP_USERNAME", ""),
		SMTPPassword:       env("SMTP_PASSWORD", ""),
		SMTPTLS:            env("SMTP_TLS", "starttls"),
		SESRegion:          env("SES_REGION", "us-east-1"),
		InboundDomain:      env("INBOUND_DOMAIN", "in.localhost"),
		InboundSecret:      env("INBOUND_SECRET", ""),
		InboundSMTPAddr:    env("INBOUND_SMTP_ADDR", ""),
		InboundAuthservIDs: envList("INBOUND_AUTHSERV_ID"),

		GoogleClientID:        env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:    env("GOOGLE_CLIENT_SECRET", ""),
		MicrosoftClientID:     env("MICROSOFT_CLIENT_ID", ""),
		MicrosoftClientSecret: env("MICROSOFT_CLIENT_SECRET", ""),
		MicrosoftTenant:       env("MICROSOFT_TENANT", "common"),
		GitHubClientID:        env("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:    env("GITHUB_CLIENT_SECRET", ""),
		OIDCIssuer:            env("OIDC_ISSUER", ""),
		OIDCClientID:          env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:      env("OIDC_CLIENT_SECRET", ""),
		OIDCName:              env("OIDC_NAME", "SSO"),

		TurnstileSiteKey: env("TURNSTILE_SITE_KEY", ""),
		TurnstileSecret:  env("TURNSTILE_SECRET", ""),
		LogoDevToken:     env("LOGO_DEV_TOKEN", ""),

		AppAttestAppIDs:      envList("APP_ATTEST_APP_IDS"),
		AppAttestDevelopment: envBool("APP_ATTEST_DEVELOPMENT", false),
		APNsKey:              os.Getenv("APNS_KEY"),
		APNsKeyID:            env("APNS_KEY_ID", ""),
		APNsTeamID:           env("APNS_TEAM_ID", ""),
		APNsTopic:            env("APNS_TOPIC", "sh.opendesk.app"),
		PushRelayURL:         strings.TrimRight(env("PUSH_RELAY_URL", ""), "/"),
		PushRelayServe:       envBool("PUSH_RELAY_SERVE", false),
		PosthogKey:           env("POSTHOG_KEY", ""),
		PosthogHost:          env("POSTHOG_HOST", "https://us.i.posthog.com"),
		EventRetention:       envDuration("EVENT_RETENTION", 7*24*time.Hour),

		StripeSecretKey:     env("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: env("STRIPE_WEBHOOK_SECRET", ""),
		StripePricePro:      env("STRIPE_PRICE_PRO", ""),
		StripePriceProYear:  env("STRIPE_PRICE_PRO_YEARLY", ""),
		StripePriceTopUp:    env("STRIPE_PRICE_AI_TOPUP", env("STRIPE_PRICE_CREDITS", "")),
		StripeAPIURL:        env("STRIPE_API_URL", ""),

		AIProvider:        env("AI_PROVIDER", ""),
		AnthropicAPIKey:   env("ANTHROPIC_API_KEY", ""),
		OpenAIAPIKey:      env("OPENAI_API_KEY", ""),
		OpenAIBaseURL:     strings.TrimRight(env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		OpenRouterAPIKey:  env("OPENROUTER_API_KEY", ""),
		OpenRouterBaseURL: strings.TrimRight(env("OPENROUTER_BASE_URL", "https://openrouter.ai/api"), "/"),
		AIModel:           env("AI_MODEL", ""),
		AIFastModel:       env("AI_FAST_MODEL", ""),
		AIModels:          envList("AI_MODELS"),
		EmbeddingsDriver:  env("EMBEDDINGS_DRIVER", ""),
		VoyageAPIKey:      env("VOYAGE_API_KEY", ""),
		EmbeddingsModel:   env("EMBEDDINGS_MODEL", ""),

		DecisionsProvider: strings.ToLower(env("DECISIONS_PROVIDER", "")),
		TypesafeAPIKey:    env("TYPESAFE_API_KEY", ""),
		TypesafeAPIURL:    strings.TrimRight(env("TYPESAFE_API_URL", "https://api.typesafe.ai"), "/"),
		TypesafeModel:     env("TYPESAFE_MODEL", ""),
	}
	markup, err := strconv.ParseFloat(env("AI_USAGE_MARKUP", env("AI_CREDIT_MARKUP", "1.5")), 64)
	if err != nil || markup < 1 || markup > 100 {
		return nil, fmt.Errorf("AI_USAGE_MARKUP must be a number from 1 to 100")
	}
	c.AIUsageMarkup = markup
	if c.AIProvider == "" {
		switch {
		case c.OpenRouterAPIKey != "":
			c.AIProvider = "openrouter"
		case c.AnthropicAPIKey != "":
			c.AIProvider = "anthropic"
		case c.OpenAIAPIKey != "":
			c.AIProvider = "openai"
		default:
			c.AIProvider = "none"
		}
	}
	if c.AIProvider == "openrouter" {
		if c.OpenRouterAPIKey == "" {
			return nil, fmt.Errorf("AI_PROVIDER=openrouter needs OPENROUTER_API_KEY")
		}
		if c.AIModel == "" {
			c.AIModel = "anthropic/claude-sonnet-5.5"
		}
		if c.AIFastModel == "" {
			c.AIFastModel = "anthropic/claude-haiku-5.5"
		}
	}
	if c.AIModel == "" && c.AIProvider == "anthropic" {
		c.AIModel = "claude-sonnet-5-5"
	}
	if c.AIFastModel == "" && c.AIProvider == "anthropic" {
		c.AIFastModel = "claude-haiku-4-5-20251001"
	}
	if c.AIFastModel == "" {
		c.AIFastModel = c.AIModel
	}
	switch c.DecisionsProvider {
	case "":
		switch {
		case c.TypesafeAPIKey != "":
			c.DecisionsProvider = "typesafe"
		case c.OpenRouterAPIKey != "":
			c.DecisionsProvider = "openrouter"
		case c.AIEnabled():
			c.DecisionsProvider = "llm"
		default:
			c.DecisionsProvider = "none"
		}
	case "typesafe":
		if c.TypesafeAPIKey == "" {
			// Without a key the LLM path keeps every feature working.
			c.DecisionsProvider = "llm"
			if !c.AIEnabled() {
				c.DecisionsProvider = "none"
			}
		}
	case "openrouter":
		if c.OpenRouterAPIKey == "" {
			return nil, fmt.Errorf("DECISIONS_PROVIDER=openrouter needs OPENROUTER_API_KEY")
		}
	case "llm", "none":
	default:
		return nil, fmt.Errorf("DECISIONS_PROVIDER must be typesafe, openrouter, llm or none")
	}
	if c.TypesafeModel == "" {
		c.TypesafeModel = "jev-latest"
		if c.DecisionsProvider == "openrouter" {
			c.TypesafeModel = "~typesafe/jev-latest"
		}
	}
	if c.EmbeddingsDriver == "" {
		switch {
		case c.VoyageAPIKey != "":
			c.EmbeddingsDriver = "voyage"
		case c.AIProvider == "openrouter":
			c.EmbeddingsDriver = "openrouter"
		case c.AIProvider == "openai":
			c.EmbeddingsDriver = "openai"
		default:
			c.EmbeddingsDriver = "none"
		}
	}
	if c.DatabaseURLDirect == "" {
		c.DatabaseURLDirect = c.DatabaseURL
	}
	c.GoogleMailboxClientID, c.GoogleMailboxClientSecret = env("GOOGLE_MAILBOX_CLIENT_ID", ""), env("GOOGLE_MAILBOX_CLIENT_SECRET", "")
	if c.GoogleMailboxClientID == "" {
		c.GoogleMailboxClientID, c.GoogleMailboxClientSecret = c.GoogleClientID, c.GoogleClientSecret
	}
	c.GoogleMailboxOAuth = mailboxOAuth("GOOGLE_MAILBOX_OAUTH", c.GoogleMailboxClientID, c.GoogleMailboxClientSecret)
	c.MicrosoftMailboxClientID, c.MicrosoftMailboxClientSecret = env("MICROSOFT_MAILBOX_CLIENT_ID", ""), env("MICROSOFT_MAILBOX_CLIENT_SECRET", "")
	if c.MicrosoftMailboxClientID == "" {
		c.MicrosoftMailboxClientID, c.MicrosoftMailboxClientSecret = c.MicrosoftClientID, c.MicrosoftClientSecret
	}
	c.MicrosoftMailboxOAuth = mailboxOAuth("MICROSOFT_MAILBOX_OAUTH", c.MicrosoftMailboxClientID, c.MicrosoftMailboxClientSecret)
	c.MailhostISPDB = envBool("MAILHOST_ISPDB", c.BillingEnabled())
	c.MailboxIMAP = envBool("MAILBOX_IMAP", true)
	c.MailboxSystemSend = envBool("MAILBOX_SYSTEM_SEND", true)
	c.Collab = envBool("COLLAB", true)
	c.CollabIMAP = envBool("COLLAB_IMAP", true)
	c.CollabGoogleClientID, c.CollabGoogleClientSecret = env("COLLAB_GOOGLE_CLIENT_ID", ""), env("COLLAB_GOOGLE_CLIENT_SECRET", "")
	if c.CollabGoogleClientID == "" {
		c.CollabGoogleClientID, c.CollabGoogleClientSecret = c.GoogleMailboxClientID, c.GoogleMailboxClientSecret
	}
	c.CollabMicrosoftClientID, c.CollabMicrosoftClientSecret = env("COLLAB_MICROSOFT_CLIENT_ID", ""), env("COLLAB_MICROSOFT_CLIENT_SECRET", "")
	if c.CollabMicrosoftClientID == "" {
		c.CollabMicrosoftClientID, c.CollabMicrosoftClientSecret = c.MicrosoftMailboxClientID, c.MicrosoftMailboxClientSecret
	}
	c.StripePriceCollab = env("STRIPE_PRICE_COLLAB", "")
	c.StripePriceCollabYear = env("STRIPE_PRICE_COLLAB_YEARLY", "")
	c.NATSStoreDir = env("NATS_STORE_DIR", c.DataDir+"/nats")
	c.CookieSecure = envBool("COOKIE_SECURE", strings.HasPrefix(c.APIURL, "https://"))
	// The binary serves the panel at <APP_URL>/admin; a development API pairs with the
	// panel's dev server.
	adminURL := c.AppURL + "/admin"
	if !c.IsProduction() && !c.ServeWeb {
		adminURL = "http://localhost:5174"
	}
	c.AdminURL = strings.TrimRight(env("ADMIN_URL", adminURL), "/")
	c.LogLevel = slog.LevelInfo
	if !c.IsProduction() {
		c.LogLevel = slog.LevelDebug
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return nil, errors.New("LOG_LEVEL must be debug, info, warn or error")
		}
	}
	if u, err := url.Parse(c.AdminURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return nil, errors.New("ADMIN_URL must be an http(s) URL, e.g. https://admin.example.com")
	}
	for _, o := range strings.Split(env("CORS_ORIGINS", c.AppURL), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, strings.TrimRight(o, "/"))
		}
	}

	if v := strings.TrimSpace(os.Getenv("TRUSTED_PROXY_HOPS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 10 {
			return nil, errors.New("TRUSTED_PROXY_HOPS must be a number from 0 to 10: the proxies in front that append to X-Forwarded-For")
		}
		c.ProxyHops = n
	}

	switch c.Role {
	case "all", "api", "worker":
	default:
		return nil, fmt.Errorf("OPENDESK_ROLE must be all, api or worker, got %q", c.Role)
	}

	secret := os.Getenv("AUTH_SECRET")
	if secret == "" && !c.IsProduction() {
		secret = "development-only-auth-secret-change-me-0000"
	}
	if len(secret) < MinAuthSecretLength {
		return nil, fmt.Errorf("AUTH_SECRET must be at least %d bytes", MinAuthSecretLength)
	}
	c.AuthSecret = []byte(secret)

	key, err := decodeKey(os.Getenv("CREDENTIALS_KEY"))
	if err != nil {
		return nil, err
	}
	if key == nil {
		if c.IsProduction() {
			return nil, errors.New("CREDENTIALS_KEY is required in production (32 bytes, hex or base64)")
		}
		key = []byte("development-only-credentials-key")
	}
	c.CredentialsKey = key

	if c.IsProduction() && c.NATSURL == "" && c.NATSToken == "" && !strings.HasPrefix(c.NATSListen, "127.0.0.1") {
		return nil, errors.New("NATS_TOKEN is required when the embedded NATS server listens beyond localhost")
	}
	if c.IsProduction() && c.StripeAPIURL != "" {
		return nil, errors.New("STRIPE_API_URL is for development only; production talks to Stripe directly")
	}
	if (c.APNsKey != "" || c.APNsKeyID != "" || c.APNsTeamID != "") && (strings.TrimSpace(c.APNsKey) == "" || c.APNsKeyID == "" || c.APNsTeamID == "") {
		return nil, errors.New("APNS_KEY, APNS_KEY_ID and APNS_TEAM_ID are required together")
	}
	if c.PushRelayServe && c.APNsKey == "" {
		return nil, errors.New("PUSH_RELAY_SERVE needs an APNs key (APNS_KEY, APNS_KEY_ID, APNS_TEAM_ID)")
	}
	if c.PushRelayURL != "" {
		u, err := url.Parse(c.PushRelayURL)
		local := u != nil && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || !(u.Scheme == "https" || (u.Scheme == "http" && local && !c.IsProduction())) {
			return nil, errors.New("PUSH_RELAY_URL must be an https origin, e.g. https://api.opendesk.sh")
		}
	}
	if err := c.loadHelp(); err != nil {
		return nil, err
	}
	c.EmailLegal = EmailLegal{Entity: env("EMAIL_LEGAL_ENTITY", ""), Number: env("EMAIL_LEGAL_COMPANY_NUMBER", ""),
		Place: env("EMAIL_LEGAL_PLACE_OF_REGISTRATION", ""), Address: env("EMAIL_LEGAL_ADDRESS", "")}
	if c.NotifyEmail, err = loadNotifyEmail(c.BillingEnabled()); err != nil {
		return nil, err
	}
	if c.BillingEnabled() && (c.StripeWebhookSecret == "" || c.StripePricePro == "") {
		return nil, errors.New("STRIPE_WEBHOOK_SECRET and STRIPE_PRICE_PRO are required when STRIPE_SECRET_KEY is set")
	}
	return c, nil
}

// loadHelp reads the help center address settings.
func (c *Config) loadHelp() error {
	c.HelpDomain = strings.TrimSuffix(strings.ToLower(env("HELP_DOMAIN", "")), ".")
	c.HelpProxySecret = os.Getenv("HELP_PROXY_SECRET")
	c.CloudflareSaaSZoneID = env("CLOUDFLARE_SAAS_ZONE_ID", "")
	c.CloudflareSaaSToken = os.Getenv("CLOUDFLARE_SAAS_TOKEN")
	if c.HelpDomain != "" && !hostnameRe.MatchString(c.HelpDomain) {
		return fmt.Errorf("HELP_DOMAIN must be a domain name such as help.example.com, got %q", c.HelpDomain)
	}
	if c.HelpProxySecret != "" && len(c.HelpProxySecret) < MinAuthSecretLength {
		return fmt.Errorf("HELP_PROXY_SECRET must be at least %d bytes", MinAuthSecretLength)
	}
	if (c.CloudflareSaaSZoneID == "") != (c.CloudflareSaaSToken == "") {
		return errors.New("CLOUDFLARE_SAAS_ZONE_ID and CLOUDFLARE_SAAS_TOKEN are required together")
	}
	target := strings.TrimSuffix(strings.ToLower(env("HELP_CNAME_TARGET", "")), ".")
	if target == "" {
		if c.CloudflareSaaS() && c.HelpDomain != "" {
			target = "custom." + c.HelpDomain
		} else if u, err := url.Parse(c.AppURL); err == nil {
			target = strings.ToLower(u.Hostname())
		}
	}
	if c.CloudflareSaaS() && !hostnameRe.MatchString(target) {
		return errors.New("HELP_CNAME_TARGET (or HELP_DOMAIN) is required with Cloudflare for SaaS")
	}
	c.HelpCNAMETarget = target
	return nil
}

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// mailboxOAuth reads a mailbox sign-in switch: off without the provider's client,
// on when unset, else the value; anything that is not a boolean turns it off.
func mailboxOAuth(k, clientID, clientSecret string) bool {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return false
	}
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

func decodeKey(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("CREDENTIALS_KEY must be 32 bytes, hex or base64 encoded")
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(k))
	if err != nil {
		return def
	}
	return v
}

func envInt(k string, def int) int {
	v, err := strconv.Atoi(os.Getenv(k))
	if err != nil {
		return def
	}
	return v
}

func envDuration(k string, def time.Duration) time.Duration {
	v, err := time.ParseDuration(os.Getenv(k))
	if err != nil {
		return def
	}
	return v
}

// envList reads a comma-separated list, dropping blanks.
func envList(k string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(k), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
