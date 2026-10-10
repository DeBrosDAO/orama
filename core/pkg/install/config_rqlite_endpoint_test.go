package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// The node.yaml the installer writes is what every on-node rqlite client
// (orama CLI, installer verification, CoreDNS seeding) resolves its endpoint
// from. It must resolve to the WireGuard IP rqlited binds, with the generated
// credentials — never to localhost, where nothing listens.
func TestGenerateNodeConfig_refusesAWGIPOutsideTheOverlay(t *testing.T) {
	cg := NewConfigGenerator(t.TempDir())
	for _, wgIP := range []string{"", "203.0.113.5", "10.1.0.5", "not-an-ip"} {
		if _, err := cg.GenerateNodeConfig(nil, wgIP, "", "node-1.dbrs.space", "dbrs.space", false); err == nil {
			t.Errorf("GenerateNodeConfig accepted WG IP %q", wgIP)
		}
	}
}

func TestGenerateNodeConfig_resolvesToTheWireGuardRQLiteEndpoint(t *testing.T) {
	oramaDir := t.TempDir()
	cg := NewConfigGenerator(oramaDir)
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig: %v", err)
	}
	if strings.Contains(out, "localhost:10100") {
		t.Errorf("node.yaml still names localhost:10100:\n%s", out)
	}

	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(out), 0600); err != nil {
		t.Fatal(err)
	}
	ep, err := rqlite.EndpointFromNodeConfig(rootfs.At(filepath.Dir(path)), path)
	if err != nil {
		t.Fatalf("resolve endpoint from the generated node.yaml: %v", err)
	}
	if ep.HostPort() != "10.0.0.5:10100" {
		t.Errorf("endpoint %s, want the WireGuard IP rqlited binds", ep)
	}

	password, err := os.ReadFile(filepath.Join(oramaDir, "secrets", "rqlite-password"))
	if err != nil {
		t.Fatal(err)
	}
	if ep.Username != "orama" || ep.Password != strings.TrimSpace(string(password)) {
		t.Errorf("endpoint credentials %q/<%d chars>, want orama and secrets/rqlite-password", ep.Username, len(ep.Password))
	}
}

// The vault config points at rqlite where it binds, not at loopback.
func TestGenerateVaultConfig_rqliteURLIsTheWireGuardAddress(t *testing.T) {
	cfg := NewConfigGenerator(t.TempDir()).GenerateVaultConfig("10.0.0.5")
	if !strings.Contains(cfg, "rqlite_url = http://10.0.0.5:10100\n") {
		t.Errorf("vault config:\n%s", cfg)
	}
}
