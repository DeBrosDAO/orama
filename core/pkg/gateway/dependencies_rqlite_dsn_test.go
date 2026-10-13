package gateway

import (
	"strings"
	"testing"
)

func TestResolveDatabaseEndpoints_explicitDSN_overridesNonEmptyDefaults(t *testing.T) {
	cfg := &Config{RQLiteDSN: "http://10.0.0.5:10000", RQLiteUsername: "orama", RQLitePassword: "pw"}
	defaults := []string{"http://10.0.0.1:10100", "http://10.0.0.2:10100"}
	got, err := resolveDatabaseEndpoints(cfg, defaults)
	if err != nil || len(got) != 1 || got[0] != "http://orama:pw@10.0.0.5:10000" {
		t.Fatalf("explicit DSN must win over DefaultClientConfig endpoints, got %v, %v", got, err)
	}
}

func TestResolveDatabaseEndpoints_emptyDSN_keepsDefaults(t *testing.T) {
	cfg := &Config{}
	defaults := []string{"http://10.0.0.1:10100"}
	got, err := resolveDatabaseEndpoints(cfg, defaults)
	if err != nil || len(got) != 1 || got[0] != defaults[0] {
		t.Fatalf("no DSN must keep defaults, got %v, %v", got, err)
	}
}

// No DSN and no defaults means no endpoint — never a guessed localhost, where
// rqlited does not listen.
func TestResolveDatabaseEndpoints_emptyEverything_isEmpty(t *testing.T) {
	if got, err := resolveDatabaseEndpoints(&Config{}, nil); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v, want no endpoints", got, err)
	}
}

// The spawner writes DSNs that already carry credentials and also sets the
// separate fields. Prefixing them again produced orama:<pw>@orama:<pw>@host,
// whose userinfo is everything before the last '@': every query was a 401.
func TestResolveDatabaseEndpoints_credentialsAppearExactlyOnce(t *testing.T) {
	for _, dsn := range []string{"http://orama:pw@10.0.0.1:10100", "http://10.0.0.1:10100"} {
		cfg := &Config{RQLiteDSN: dsn, RQLiteUsername: "orama", RQLitePassword: "pw"}
		got, err := resolveDatabaseEndpoints(cfg, nil)
		if err != nil {
			t.Fatalf("%s: %v", dsn, err)
		}
		if got[0] != "http://orama:pw@10.0.0.1:10100" || strings.Count(got[0], "@") != 1 {
			t.Errorf("%s -> %q, want credentials exactly once", dsn, got[0])
		}
	}
}

func TestResolveDatabaseEndpoints_DSNWithoutAnyCredentialsIsAnError(t *testing.T) {
	if _, err := resolveDatabaseEndpoints(&Config{RQLiteDSN: "http://10.0.0.1:10100"}, nil); err == nil {
		t.Fatal("rqlited always requires auth; a DSN with no credentials anywhere must be refused")
	}
}
