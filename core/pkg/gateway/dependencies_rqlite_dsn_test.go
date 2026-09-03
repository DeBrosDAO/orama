package gateway

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestResolveDatabaseEndpoints_explicitDSN_overridesNonEmptyDefaults(t *testing.T) {
	cfg := &Config{RQLiteDSN: "http://localhost:10000"}
	defaults := []string{"http://10.0.0.1:10100", "http://10.0.0.2:10100"}
	got := resolveDatabaseEndpoints(cfg, defaults)
	if len(got) != 1 || got[0] != "http://localhost:10000" {
		t.Fatalf("explicit DSN must win over DefaultClientConfig endpoints, got %v", got)
	}
}

func TestResolveDatabaseEndpoints_emptyDSN_keepsDefaults(t *testing.T) {
	cfg := &Config{}
	defaults := []string{"http://10.0.0.1:10100"}
	got := resolveDatabaseEndpoints(cfg, defaults)
	if len(got) != 1 || got[0] != defaults[0] {
		t.Fatalf("no DSN must keep defaults, got %v", got)
	}
}

func TestResolveDatabaseEndpoints_emptyEverything_fallsBackToIndexPort(t *testing.T) {
	cfg := &Config{}
	got := resolveDatabaseEndpoints(cfg, nil)
	want := fmt.Sprintf("http://localhost:%d", constants.RQLiteHTTPPort)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestResolveDatabaseEndpoints_injectsAuth(t *testing.T) {
	cfg := &Config{RQLiteDSN: "http://localhost:10000", RQLiteUsername: "orama", RQLitePassword: "s3cret"}
	got := resolveDatabaseEndpoints(cfg, []string{"http://10.0.0.1:10100"})
	if len(got) != 1 || got[0] != "http://orama:s3cret@localhost:10000" {
		t.Fatalf("got %v", got)
	}
}
