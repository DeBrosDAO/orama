package gateway

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// A member added on one node signed in on another got 403 NAMESPACE_NOT_OWNED:
// the registry client read grants at level=none, from the follower's own
// replica, which had not applied the leader's acknowledged write yet. The
// handles auth reads grants, nonces and API keys through must read at the
// leader.
func TestRegistryClientConfig_readsAtTheLeader(t *testing.T) {
	cfg := &Config{
		RQLiteDSN:       "http://10.0.0.5:15000",
		GlobalRQLiteDSN: "http://10.0.0.5:10100",
		RQLiteUsername:  "orama",
		RQLitePassword:  "test-pw",
	}
	got, err := registryClientConfig(cfg)
	if err != nil {
		t.Fatalf("registryClientConfig: %v", err)
	}
	if got.DatabaseReadLevel != client.ReadLevelWeak {
		t.Errorf("registry read level = %q, want %q", got.DatabaseReadLevel, client.ReadLevelWeak)
	}
	if len(got.DatabaseEndpoints) != 1 {
		t.Errorf("registry endpoints = %v, want the one global DSN", got.DatabaseEndpoints)
	}
}

func TestRegistryClientConfig_refusesAMalformedDSN(t *testing.T) {
	if _, err := registryClientConfig(&Config{GlobalRQLiteDSN: "http://[::1"}); err == nil {
		t.Fatal("a malformed global_rqlite_dsn was accepted")
	}
}

// On the index gateway the registry is its own database, read through the main
// client, so that client reads at the leader too.
func TestGatewayClientConfig_readsAtTheLeader(t *testing.T) {
	got, err := gatewayClientConfig(&Config{
		ClientNamespace: "default",
		RQLiteDSN:       "http://localhost:10100",
		RQLiteUsername:  "orama",
		RQLitePassword:  "test-pw",
	})
	if err != nil {
		t.Fatalf("gatewayClientConfig: %v", err)
	}
	if got.DatabaseReadLevel != client.ReadLevelWeak {
		t.Errorf("gateway client read level = %q, want %q", got.DatabaseReadLevel, client.ReadLevelWeak)
	}
}
