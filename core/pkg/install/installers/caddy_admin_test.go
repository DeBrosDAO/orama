package installers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func caddyUnit(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "systemd", "orama-namespace-caddy@.service"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The finding: nothing set `admin`, so Caddy's admin API listened on
// localhost:2019 — an unauthenticated way for any process on the host to
// replace the config of the process that terminates TLS.
func TestGenerateCaddyfile_adminAPIIsAPrivateSocket(t *testing.T) {
	cf := NewCaddyInstaller("amd64", nil, "/opt/orama").generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space", "")
	want := "    admin unix/" + CaddyAdminSocket + "|0600\n"
	global := cf[:strings.Index(cf, "\n}\n")]
	if !strings.Contains(global, want) {
		t.Fatalf("global options do not bind the admin API to the private socket:\n%s", global)
	}
	if strings.Contains(cf, "2019") {
		t.Error("the Caddyfile mentions the default admin port")
	}
}

// The socket lives in the unit's runtime directory, which systemd creates 0700
// for the orama user; ExecReload has to reach the same socket.
func TestCaddyUnit_runtimeDirectoryHoldsTheAdminSocket(t *testing.T) {
	unit := caddyUnit(t)
	dir := filepath.Base(filepath.Dir(CaddyAdminSocket))
	if filepath.Dir(filepath.Dir(CaddyAdminSocket)) != "/run" {
		t.Fatalf("CaddyAdminSocket %s is not directly in a /run runtime directory", CaddyAdminSocket)
	}
	for _, want := range []string{
		"RuntimeDirectory=" + dir + "\n",
		"RuntimeDirectoryMode=0700\n",
		"ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --address unix/" + CaddyAdminSocket + "\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("the Caddy unit lacks %q", strings.TrimSpace(want))
		}
	}
}

// Caddy's DNS provider signs its calls with the key install writes; a
// Caddyfile without key_file would make every certificate renewal fail.
func TestGenerateCaddyfile_dnsProviderSignsWithTheACMEKey(t *testing.T) {
	cf := NewCaddyInstaller("amd64", nil, "/opt/orama").generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space", "")
	blocks := strings.Count(cf, "dns orama {")
	if blocks == 0 {
		t.Fatal("no orama DNS provider block")
	}
	if got := strings.Count(cf, "key_file "+CaddyACMEKeyPath+"\n"); got != blocks {
		t.Errorf("%d of %d DNS provider blocks name the ACME key", got, blocks)
	}
}
