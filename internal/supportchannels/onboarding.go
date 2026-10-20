package supportchannels

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

const healthFailure = "Couldn't verify the provider setup. Check the credentials and provider settings."

// reconcileConnection uses only optional adapter capabilities. A future
// provider opts into setup, discovery and health independently without adding
// provider-specific branches to the service.
func (s *Service) reconcileConnection(ctx context.Context, c connection, credentials map[string]string) connection {
	a, ok := s.registry.Get(c.Provider)
	if !ok {
		return s.storeHealth(ctx, c, channel.Health{Status: "error", Detail: healthFailure})
	}
	account, err := a.Validate(ctx, s.http, credentials)
	if err != nil {
		return s.storeHealth(ctx, c, channel.Health{Status: "error", Detail: "The provider refused the credentials. Replace them with working ones."})
	}
	if account.ID != c.ExternalAccountID {
		return s.storeHealth(ctx, c, channel.Health{Status: "error", Detail: "These credentials belong to another provider account."})
	}
	c = s.refreshAccount(ctx, c, cleanAccount(a, account))
	setup := channel.Setup{WebhookURL: s.webhookURL(c), Foreign: func(url string) bool { return s.foreignWebhook(ctx, c, url) }}
	if configurer, ok := a.(channel.Configurer); ok {
		if err := configurer.Configure(ctx, s.http, credentials, setup); errors.Is(err, channel.ErrInUse) {
			return s.storeHealth(ctx, c, channel.Health{Status: "action_required", Detail: inUse})
		} else if err != nil {
			s.d.Log.Warn("support channel setup failed", "provider", c.Provider, "connection_id", c.ID)
			return s.storeHealth(ctx, c, channel.Health{Status: "error", Detail: healthFailure})
		}
	}
	if checker, ok := a.(channel.HealthChecker); ok {
		health, err := checker.Health(ctx, s.http, credentials, setup)
		if errors.Is(err, channel.ErrInUse) {
			return s.storeHealth(ctx, c, channel.Health{Status: "action_required", Detail: inUse})
		}
		if err != nil {
			s.d.Log.Warn("support channel health check failed", "provider", c.Provider, "connection_id", c.ID)
			return s.storeHealth(ctx, c, channel.Health{Status: "error", Detail: healthFailure})
		}
		// Providers without an automatic configuration API cannot query their
		// inbound URL. Once a signed callback has proved it, a credential test
		// must not downgrade that evidence to action_required.
		if _, configurable := a.(channel.Configurer); !configurable && health.Status == "action_required" && c.HealthStatus == "healthy" {
			return c
		}
		return s.storeHealth(ctx, c, health)
	}
	return s.storeHealth(ctx, c, channel.Health{Status: "healthy", Detail: "The provider accepted the connection."})
}

const inUse = "This bot already serves another opendesk connection. Disconnect that one, or use a separate bot for each account, then test again."

// refreshAccount keeps the stored account name and handle current: a bot can be
// renamed, and connections made before handles were kept get theirs.
func (s *Service) refreshAccount(ctx context.Context, c connection, account channel.Account) connection {
	if account.Name == c.ExternalAccountName && account.Handle == c.ExternalAccountHandle {
		return c
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET external_account_name=$2,external_account_handle=$3 WHERE id=$1`, c.ID, account.Name, account.Handle); err != nil {
		s.d.Log.Warn("support channel account", "connection_id", c.ID, "err", err)
		return c
	}
	c.ExternalAccountName, c.ExternalAccountHandle = account.Name, account.Handle
	return c
}

// foreignWebhook reports whether url is the webhook of another connection that
// still exists on this instance.
func (s *Service) foreignWebhook(ctx context.Context, c connection, url string) bool {
	prefix := strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/support-channels/hooks/" + c.Provider + "/"
	id, err := uuid.Parse(strings.TrimPrefix(url, prefix))
	if !strings.HasPrefix(url, prefix) || err != nil || id == c.ID {
		return false
	}
	var ok bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_channel_connections WHERE id=$1)`, id).Scan(&ok); err != nil {
		s.d.Log.Warn("support channel webhook owner", "connection_id", c.ID, "err", err)
		return true
	}
	return ok
}

func (s *Service) markWebhookHealthy(ctx context.Context, c connection) {
	if c.HealthStatus == "healthy" {
		return
	}
	_, err := s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET health_status='healthy',health_detail='A signed webhook was received.',health_checked_at=now() WHERE id=$1 AND health_status<>'healthy'`, c.ID)
	if err != nil {
		s.d.Log.Warn("support channel webhook health", "connection_id", c.ID, "err", err)
	}
}

func (s *Service) storeHealth(ctx context.Context, c connection, health channel.Health) connection {
	if health.Status != "healthy" && health.Status != "action_required" && health.Status != "error" {
		health = channel.Health{Status: "error", Detail: healthFailure}
	}
	now := time.Now().UTC()
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET health_status=$2,health_detail=$3,health_checked_at=$4 WHERE id=$1`, c.ID, health.Status, health.Detail, now); err != nil {
		s.d.Log.Warn("support channel health status", "connection_id", c.ID, "err", err)
		return c
	}
	c.HealthStatus, c.HealthDetail, c.HealthCheckedAt = health.Status, health.Detail, &now
	return c
}

func (s *Service) discoverDestinations(ctx context.Context, c connection, credentials map[string]string) ([]channel.Destination, error) {
	a, _ := s.registry.Get(c.Provider)
	if discoverer, ok := a.(channel.Discoverer); ok {
		items, err := discoverer.Discover(ctx, s.http, credentials)
		if err != nil {
			return nil, err
		}
		if err = s.storeDiscovered(ctx, c, items); err != nil {
			return nil, err
		}
		return availableDestinations(items), nil
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT external_channel_id,external_channel_name,kind,available FROM support_channel_discovered_destinations WHERE workspace_id=$1 AND connection_id=$2 AND available=true ORDER BY external_channel_name,external_channel_id`, c.WorkspaceID, c.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (channel.Destination, error) {
		var item channel.Destination
		err := r.Scan(&item.ID, &item.Name, &item.Kind, &item.Available)
		return item, err
	})
}

func availableDestinations(items []channel.Destination) []channel.Destination {
	out := make([]channel.Destination, 0, len(items))
	for _, item := range items {
		if item.ID != "" && item.Available {
			item.Name = cleanExternalName(item.Name)
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Service) storeDiscovered(ctx context.Context, c connection, items []channel.Destination) error {
	if len(items) == 0 {
		return nil
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		for _, item := range items {
			if item.ID == "" {
				continue
			}
			item.Name = cleanExternalName(item.Name)
			if _, err := tx.Exec(ctx, `INSERT INTO support_channel_discovered_destinations(workspace_id,connection_id,external_channel_id,external_channel_name,kind,available) VALUES($1,$2,$3,$4,$5,$6)
				ON CONFLICT(connection_id,external_channel_id) DO UPDATE SET external_channel_name=EXCLUDED.external_channel_name,kind=EXCLUDED.kind,available=EXCLUDED.available,updated_at=now()`, c.WorkspaceID, c.ID, item.ID, item.Name, item.Kind, item.Available); err != nil {
				return err
			}
		}
		return nil
	})
}

func cleanExternalName(name string) string {
	name = displayname.Displayable(name)
	runes := []rune(name)
	if len(runes) > 100 {
		name = string(runes[:100])
	}
	return name
}

func (s *Service) loadWorkspaceConnection(ctx context.Context, wid, id uuid.UUID) (connection, error) {
	var c connection
	err := s.d.DB.Pool.QueryRow(ctx, connectionSelect+` WHERE workspace_id=$1 AND id=$2`, wid, id).Scan(connectionScan(&c)...)
	if err == nil {
		c.WebhookURL = s.webhookURL(c)
	}
	return c, err
}
