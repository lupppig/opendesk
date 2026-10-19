package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/mailer"
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/storage"
)

// BuildTool is Build for a command-line tool that writes data next to a running instance
// (opendesk admin test-workspace): the same database, storage, keys and plan rules, but no
// NATS connection (so no embedded server is started) and a job client that only inserts;
// the instance's workers run what it queues.
func BuildTool(ctx context.Context, cfg *config.Config, log *slog.Logger) (*deps.Deps, func(), error) {
	database, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabaseURLDirect)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*deps.Deps, func(), error) {
		database.Close()
		return nil, nil, err
	}
	store, err := storage.New(ctx, storage.Options{Driver: cfg.StorageDriver, Dir: cfg.DataDir + "/files", Bucket: cfg.S3Bucket,
		Region: cfg.S3Region, Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePathStyle})
	if err != nil {
		return fail(err)
	}
	m, err := mailer.New(ctx, mailer.Options{Driver: cfg.MailDriver, From: cfg.MailFrom, Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLS: cfg.SMTPTLS, Region: cfg.SESRegion}, log)
	if err != nil {
		return fail(err)
	}
	box, err := crypto.NewBox(cfg.CredentialsKey)
	if err != nil {
		return fail(err)
	}
	d := &deps.Deps{Cfg: cfg, Log: log, DB: database, Storage: store, Mailer: m, Analytics: analytics.New("", "", log), Box: box,
		Tokens: auth.NewTokens(cfg.AuthSecret), Captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Disabled: true}),
		Limiter: httpx.NewLimiter(), Plan: plan.New(cfg.BillingEnabled(), cfg.AIUsageMarkup)}
	d.Plan.CollabOff = !cfg.Collab
	domain.Configure(cfg.APIURL, cfg.AuthSecret)
	domain.ConfigureSending(cfg.MailboxSystemSend)
	if d.Jobs, err = river.NewClient[pgx.Tx](riverpgxv5.New(database.Direct), &river.Config{}); err != nil {
		return fail(fmt.Errorf("river: %w", err))
	}
	return d, func() { d.Analytics.Close(); database.Close() }, nil
}
