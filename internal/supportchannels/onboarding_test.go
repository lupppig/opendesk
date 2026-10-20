package supportchannels

import (
	"testing"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestCleanCredentialsGeneratesProviderOwnedSecret(t *testing.T) {
	descriptor := channel.Descriptor{Fields: []channel.Field{
		{Name: "token"},
		{Name: "secret", Generated: true},
	}}
	got := cleanCredentials(descriptor, map[string]string{"token": " value "})
	if got == nil || got["token"] != "value" || len(got["secret"]) != 48 {
		t.Fatalf("credentials: %#v", got)
	}
	if cleanCredentials(descriptor, map[string]string{}) != nil {
		t.Fatal("accepted a missing user-supplied credential")
	}
}

func TestDiscoveryCredentialsAllowOnlyInitialFields(t *testing.T) {
	descriptor := channel.Descriptor{Fields: []channel.Field{
		{Name: "token"},
		{Name: "account", Discovered: true},
		{Name: "secret", Generated: true},
	}}
	got := discoveryCredentials(descriptor, map[string]string{"token": " value ", "account": "G1", "secret": "x", "extra": "y"})
	if len(got) != 1 || got["token"] != "value" {
		t.Fatalf("credentials: %#v", got)
	}
}

func TestAvailableDestinationsFiltersAndSorts(t *testing.T) {
	got := availableDestinations([]channel.Destination{
		{ID: "2", Name: "zeta", Available: true},
		{ID: "3", Name: "hidden"},
		{ID: "1", Name: "alpha", Available: true},
		{Name: "missing id", Available: true},
	})
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("destinations: %+v", got)
	}
}
