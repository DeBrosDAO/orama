package installers

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestSNIRouterInstaller returns an installer rooted at a temp oramaDir so
// Configure writes to an isolated location.
func newTestSNIRouterInstaller(oramaDir string) *SNIRouterInstaller {
	return NewSNIRouterInstaller("amd64", io.Discard, oramaDir)
}

// TestGenerateConfig_includesDiscoveryAndFallback verifies the rendered
// sni-router.yaml binds :443, falls back to Caddy on the moved HTTPS port, and
// emits a turn_discovery block pointing at the node's namespaces dir + base
// domain.
func TestGenerateConfig_includesDiscoveryAndFallback(t *testing.T) {
	dir := t.TempDir()
	si := newTestSNIRouterInstaller(dir)

	cfg := si.generateConfig("orama-devnet.network")

	for _, want := range []string{
		`listen: ":443"`,
		"fallback:",
		`addr: "127.0.0.1:8443"`,
		"turn_discovery:",
		"base_domain: \"orama-devnet.network\"",
		"rescan_interval: 30s",
		"routes: []",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("generated sni-router config missing %q\n---\n%s", want, cfg)
		}
	}

	// namespaces_dir must be the node's data/namespaces path.
	wantNS := filepath.Join(dir, "data", "namespaces")
	if !strings.Contains(cfg, wantNS) {
		t.Errorf("config missing namespaces_dir %q\n---\n%s", wantNS, cfg)
	}
}

// TestConfigure_writesFileToConfigsDir verifies Configure persists the YAML to
// <oramaDir>/configs/sni-router.yaml.
func TestConfigure_writesFileToConfigsDir(t *testing.T) {
	dir := t.TempDir()
	si := newTestSNIRouterInstaller(dir)

	if err := si.Configure("example.com"); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}

	path := filepath.Join(dir, "configs", "sni-router.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected config at %s: %v", path, err)
	}
	if !strings.Contains(string(data), "base_domain: \"example.com\"") {
		t.Errorf("written config missing base_domain; got:\n%s", string(data))
	}
}

// TestConfigure_rejectsEmptyBaseDomain verifies the installer refuses an empty
// base domain rather than emitting a config that would derive bogus hostnames.
func TestConfigure_rejectsEmptyBaseDomain(t *testing.T) {
	si := newTestSNIRouterInstaller(t.TempDir())
	if err := si.Configure(""); err == nil {
		t.Errorf("expected error for empty base domain")
	}
}

// TestGenerateSystemdUnit_shape verifies the unit grants CAP_NET_BIND_SERVICE,
// runs as orama, restarts on failure, and points ExecStart at the installed
// binary + config.
func TestGenerateSystemdUnit_shape(t *testing.T) {
	dir := t.TempDir()
	si := newTestSNIRouterInstaller(dir)
	unit := si.generateSystemdUnit()

	for _, want := range []string{
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"User=orama",
		"Restart=on-failure",
		"EnvironmentFile=-/opt/orama/.orama/data/sni-router.env",
		// ExecStart must point at the ABSOLUTE config path so it doesn't
		// depend on WorkingDirectory/$HOME resolution at runtime.
		"ExecStart=/opt/orama/bin/orama-sni-router --config " + si.configPath(),
		"Before=caddy.service",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("systemd unit missing %q\n---\n%s", want, unit)
		}
	}
	if !strings.Contains(si.configPath(), dir) {
		t.Errorf("configPath %q not rooted at the oramaDir %q", si.configPath(), dir)
	}
}
