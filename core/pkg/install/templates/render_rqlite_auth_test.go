package templates

import (
	"strings"
	"testing"
)

func TestRenderNodeConfig_rqliteCredentials(t *testing.T) {
	data := NodeConfigData{
		NodeID:         "node2",
		P2PPort:        4002,
		DataDir:        "/opt/orama/.orama/node2",
		RQLiteHTTPPort: 5002,
		RQLiteRaftPort: 7002,
		RQLiteUsername: "orama",
		RQLitePassword: "deadbeef",
		RQLiteAuthFile: "/home/orama/.orama/secrets/rqlite-auth.json",
	}

	result, err := RenderNodeConfig(data)
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}

	for _, want := range []string{
		`rqlite_username: "orama"`,
		`rqlite_password: "deadbeef"`,
		`rqlite_auth_file: "/home/orama/.orama/secrets/rqlite-auth.json"`,
	} {
		if !strings.Contains(result, want) {
			t.Errorf("node config missing %s\n%s", want, result)
		}
	}

	// Enforcement is never rendered. It is switched on by an operator once the
	// whole fleet is sending credentials; emitting it here would enable it on
	// the first node regenerated, mid-upgrade.
	if strings.Contains(result, "rqlite_enforce_auth") {
		t.Errorf("template rendered rqlite_enforce_auth:\n%s", result)
	}
}

// With no credentials known, the keys must be absent rather than rendered
// empty: `rqlite_auth_file: ""` and an unset field mean the same thing to the
// loader, but an empty path in a config file reads as a broken installation.
func TestRenderNodeConfig_omitsEmptyRQLiteCredentials(t *testing.T) {
	result, err := RenderNodeConfig(NodeConfigData{
		NodeID:         "node2",
		P2PPort:        4002,
		DataDir:        "/opt/orama/.orama/node2",
		RQLiteHTTPPort: 5002,
		RQLiteRaftPort: 7002,
	})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	for _, unwanted := range []string{"rqlite_username", "rqlite_password", "rqlite_auth_file"} {
		if strings.Contains(result, unwanted) {
			t.Errorf("rendered %s with nothing to put in it:\n%s", unwanted, result)
		}
	}
}
