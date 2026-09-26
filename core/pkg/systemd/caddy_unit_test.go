package systemd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readCaddyTemplate(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "orama-namespace-caddy@.service"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Caddy waited for the gateway's /health and exited when it did not answer, so
// a gateway that was down took HTTPS down with it; a second gate waited on
// `dig`, which is not installed. Caddy retries upstreams and ACME itself.
func TestCaddyTemplate_hasNoStartGates(t *testing.T) {
	if strings.Contains(readCaddyTemplate(t), "ExecStartPre=") {
		t.Error("the Caddy unit gates its start on something; Caddy must come up whatever its upstreams are doing")
	}
}

// Caddy autosaves under XDG_CONFIG_HOME; the default ($HOME/.config) is
// read-only under ProtectSystem=strict.
func TestCaddyTemplate_configHomeIsWritable(t *testing.T) {
	unit := readCaddyTemplate(t)
	if !strings.Contains(unit, "Environment=XDG_CONFIG_HOME=/var/lib/caddy/") {
		t.Fatal("XDG_CONFIG_HOME is not set inside /var/lib/caddy")
	}
	if !strings.Contains(unit, "ReadWritePaths=") || !strings.Contains(unit, " /var/lib/caddy") {
		t.Error("/var/lib/caddy is not writable to the unit")
	}
}
