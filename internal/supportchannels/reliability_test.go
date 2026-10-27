package supportchannels

import (
	"net/http"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestUnhealthyConnectionAlertsAdminsOnceUntilRecovery(t *testing.T) {
	e := hdtest.New(t)
	ws := e.NewWorkspace()
	s := For(e.D)
	c := connection{ID: ids.New(), WorkspaceID: ws.ID, Provider: "slack", Name: "Support", HealthStatus: "error", HealthDetail: "Check the provider setup."}
	e.Exec(`INSERT INTO support_channel_connections(id,workspace_id,provider,name,external_account_id,credentials_enc,health_status,health_detail,health_checked_at,health_unhealthy_since)
		VALUES($1,$2,'slack',$3,'T1',$4,'error',$5,now()-interval '20 minutes',now()-interval '20 minutes')`, c.ID, ws.ID, c.Name, []byte{0}, c.HealthDetail)

	if err := s.alertUnhealthyConnections(e.Ctx); err != nil {
		t.Fatal(err)
	}
	var alerts int
	e.Scalar(`SELECT count(*) FROM notifications WHERE workspace_id=$1 AND type=$2`, &alerts, ws.ID, NotifySupportChannelUnhealthy)
	if alerts != 2 {
		t.Fatalf("alerts = %d, want owner and admin", alerts)
	}
	e.Scalar(`SELECT count(*) FROM notifications WHERE workspace_id=$1 AND type=$2 AND user_id=$3`, &alerts, ws.ID, NotifySupportChannelUnhealthy, ws.Agent.ID)
	if alerts != 0 {
		t.Fatalf("agent alerts = %d", alerts)
	}
	if err := s.alertUnhealthyConnections(e.Ctx); err != nil {
		t.Fatal(err)
	}
	e.Scalar(`SELECT count(*) FROM notifications WHERE workspace_id=$1 AND type=$2`, &alerts, ws.ID, NotifySupportChannelUnhealthy)
	if alerts != 2 {
		t.Fatalf("repeated alerts = %d", alerts)
	}

	c, err := s.loadConnection(e.Ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c = s.storeHealth(e.Ctx, c, channel.Health{Status: "healthy", Detail: "Working."})
	if c.HealthUnhealthySince != nil || c.HealthAlertedAt != nil {
		t.Fatalf("recovery kept unhealthy episode: %+v", c)
	}
	c = s.storeHealth(e.Ctx, c, channel.Health{Status: "error", Detail: "Check the provider setup."})
	if c.HealthUnhealthySince == nil || c.HealthAlertedAt != nil {
		t.Fatalf("new unhealthy episode = %+v", c)
	}
	if err := s.alertUnhealthyConnections(e.Ctx); err != nil {
		t.Fatal(err)
	}
	e.Scalar(`SELECT count(*) FROM notifications WHERE workspace_id=$1 AND type=$2`, &alerts, ws.ID, NotifySupportChannelUnhealthy)
	if alerts != 2 {
		t.Fatalf("fresh episode alerted too soon: %d", alerts)
	}
}

func TestOperationsReportDeliveryAndWebhookMetrics(t *testing.T) {
	e := hdtest.New(t, Register)
	ws := e.NewWorkspace()
	s := For(e.D)
	cid := ids.New()
	e.Exec(`INSERT INTO support_channel_connections(id,workspace_id,provider,name,external_account_id,credentials_enc,health_status,health_checked_at)
		VALUES($1,$2,'slack','Support','T1',$3,'healthy',now())`, cid, ws.ID, []byte{0})

	for seq, status := range map[int64]string{101: "queued", 102: "retrying", 103: "delivered", 104: "failed"} {
		e.Exec(`INSERT INTO events(workspace_id,seq,id,type,visibility,topics,data,created_at)
			VALUES($1,$2,$3,'ticket.updated','agents','{}','{}',now()-interval '10 seconds')`, ws.ID, seq, ids.New())
		e.Exec(`INSERT INTO support_channel_dispatches(workspace_id,event_seq,status,attempts,created_at,updated_at,delivered_at)
			VALUES($1,$2,$3,1,now()-interval '10 seconds',now(),CASE WHEN $3='delivered' THEN now() ELSE NULL END)`, ws.ID, seq, status)
	}
	s.recordWebhookRejection(e.Ctx, cid)
	s.recordWebhookRejection(e.Ctx, cid)
	c, err := s.loadConnection(e.Ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	in := channel.Inbound{EventID: "duplicate-1", ExternalChannelID: "C1"}
	if err := s.persistInbound(e.Ctx, c, []channel.Inbound{in}); err != nil {
		t.Fatal(err)
	}
	if err := s.persistInbound(e.Ctx, c, []channel.Inbound{in}); err != nil {
		t.Fatal(err)
	}

	var got operations
	e.Do(http.MethodGet, "/v1/support-channels/operations", ws.Admin.As(ws), nil).Want(t, http.StatusOK).JSON(t, &got)
	if got.Connections.Total != 1 || got.Connections.Healthy != 1 || got.Connections.Unhealthy != 0 {
		t.Fatalf("connection metrics = %+v", got.Connections)
	}
	if got.Deliveries.Queued != 1 || got.Deliveries.Retrying != 1 || got.Deliveries.Delivered != 1 || got.Deliveries.Failed != 1 {
		t.Fatalf("delivery metrics = %+v", got.Deliveries)
	}
	if got.Deliveries.AverageLatencyMS < int64((9*time.Second)/time.Millisecond) || got.Deliveries.MaximumLatencyMS < got.Deliveries.AverageLatencyMS || got.Deliveries.OldestPendingAt == nil {
		t.Fatalf("latency metrics = %+v", got.Deliveries)
	}
	if got.Webhooks.Rejected != 2 || got.Webhooks.DuplicatesSuppressed != 1 {
		t.Fatalf("webhook metrics = %+v", got.Webhooks)
	}
	e.Do(http.MethodGet, "/v1/support-channels/operations", ws.Agent.As(ws), nil).Want(t, http.StatusForbidden)
}
