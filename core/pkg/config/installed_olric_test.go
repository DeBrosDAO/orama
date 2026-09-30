package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNodeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Olric binds the WireGuard address, so the doctor's localhost probe failed on every healthy
// stagenet node; the address comes from the installed config.
func TestInstalledOlricURL_isTheConfiguredWireGuardAddress(t *testing.T) {
	path := writeNodeConfig(t, "http_gateway:\n  olric_servers:\n    - \"10.0.0.2:10102\"\n    - \"10.0.0.1:10102\"\n")
	got, err := InstalledOlricURL(path)
	if err != nil || got != "http://10.0.0.2:10102" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestInstalledOlricURL_refusesAMissingOrBadAddress(t *testing.T) {
	for name, body := range map[string]string{
		"no servers":   "http_gateway:\n  enabled: true\n",
		"empty list":   "http_gateway:\n  olric_servers: []\n",
		"no port":      "http_gateway:\n  olric_servers: [\"10.0.0.2\"]\n",
		"not yaml":     "http_gateway: [",
		"empty config": "",
	} {
		if _, err := InstalledOlricURL(writeNodeConfig(t, body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := InstalledOlricURL(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "installed node") {
		t.Errorf("missing file: %v", err)
	}
}
