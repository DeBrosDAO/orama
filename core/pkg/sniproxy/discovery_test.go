package sniproxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/turn"
)

// writeTURNConfig is a test helper that lays out the on-disk shape the namespace
// spawner produces: <namespacesDir>/<namespace>/configs/turn-<nodeID>.yaml.
func writeTURNConfig(t *testing.T, namespacesDir, namespace, nodeID, turnsAddr string) {
	t.Helper()
	configDir := filepath.Join(namespacesDir, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir configs failed: %v", err)
	}
	content := "namespace: \"" + namespace + "\"\n"
	content += "turns_listen_addr: \"" + turnsAddr + "\"\n"
	path := filepath.Join(configDir, "turn-"+nodeID+".yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write turn config failed: %v", err)
	}
}

// TestDiscoverTURNRoutes_scansFixtureDir verifies that two namespaces each with
// a TURNS listener yield two routes apiece (stealth host + turn.ns-* alias),
// while a namespace with an empty turns_listen_addr is skipped entirely.
func TestDiscoverTURNRoutes_scansFixtureDir(t *testing.T) {
	dir := t.TempDir()
	const base = "orama-devnet.network"

	writeTURNConfig(t, dir, "anchat", "node-1", "0.0.0.0:5349")
	writeTURNConfig(t, dir, "video", "node-1", "0.0.0.0:5350")
	// TURNS disabled — must produce no routes.
	writeTURNConfig(t, dir, "noturns", "node-1", "")

	routes, err := DiscoverTURNRoutes(TURNDiscoveryConfig{
		NamespacesDir: dir,
		BaseDomain:    base,
	}, nil)
	if err != nil {
		t.Fatalf("DiscoverTURNRoutes failed: %v", err)
	}

	// 2 namespaces with TURNS × 2 routes each = 4.
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d: %+v", len(routes), routes)
	}

	got := map[string]string{}
	for _, r := range routes {
		got[r.Match] = r.Backend.Addr
	}

	// anchat: backend port 5349, stealth host + alias.
	anchatStealth := turn.StealthHostForNamespace("anchat", base)
	if got[anchatStealth] != "127.0.0.1:5349" {
		t.Errorf("anchat stealth route missing/wrong: %q -> %q", anchatStealth, got[anchatStealth])
	}
	if got["turn.ns-anchat."+base] != "127.0.0.1:5349" {
		t.Errorf("anchat alias route missing/wrong: got %q", got["turn.ns-anchat."+base])
	}

	// video: backend port 5350.
	videoStealth := turn.StealthHostForNamespace("video", base)
	if got[videoStealth] != "127.0.0.1:5350" {
		t.Errorf("video stealth route missing/wrong: %q -> %q", videoStealth, got[videoStealth])
	}
	if got["turn.ns-video."+base] != "127.0.0.1:5350" {
		t.Errorf("video alias route missing/wrong: got %q", got["turn.ns-video."+base])
	}

	// The disabled namespace must not appear under any of its hostnames.
	if _, ok := got["turn.ns-noturns."+base]; ok {
		t.Errorf("noturns namespace should be skipped (empty turns_listen_addr)")
	}
}

// TestDiscoverTURNRoutes_emptyTURNSAddrSkipped is a focused check that a single
// namespace with an empty turns_listen_addr produces zero routes (no error).
func TestDiscoverTURNRoutes_emptyTURNSAddrSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTURNConfig(t, dir, "noturns", "node-1", "")

	routes, err := DiscoverTURNRoutes(TURNDiscoveryConfig{
		NamespacesDir: dir,
		BaseDomain:    "example.com",
	}, nil)
	if err != nil {
		t.Fatalf("DiscoverTURNRoutes failed: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for TURNS-disabled namespace, got %d: %+v", len(routes), routes)
	}
}

// TestDiscoverTURNRoutes_unreadableDirReturnsError verifies a missing namespaces
// directory is a transient error (so callers keep previous routes), not a silent
// empty result.
func TestDiscoverTURNRoutes_unreadableDirReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	routes, err := DiscoverTURNRoutes(TURNDiscoveryConfig{
		NamespacesDir: missing,
		BaseDomain:    "example.com",
	}, nil)
	if err == nil {
		t.Fatalf("expected an error for unreadable namespaces dir, got nil (routes=%+v)", routes)
	}
	if routes != nil {
		t.Errorf("expected nil routes on error, got %+v", routes)
	}
}

// TestDiscoverTURNRoutes_malformedFileSkipped verifies one unparseable
// turn-*.yaml is skipped while a sibling valid namespace still yields routes
// (one bad file must not hide the rest).
func TestDiscoverTURNRoutes_malformedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	const base = "example.com"

	writeTURNConfig(t, dir, "good", "node-1", "0.0.0.0:5349")

	badDir := filepath.Join(dir, "bad", "configs")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatalf("mkdir bad configs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "turn-node-1.yaml"), []byte(":\n  not: [valid"), 0644); err != nil {
		t.Fatalf("write malformed config failed: %v", err)
	}

	routes, err := DiscoverTURNRoutes(TURNDiscoveryConfig{
		NamespacesDir: dir,
		BaseDomain:    base,
	}, nil)
	if err != nil {
		t.Fatalf("DiscoverTURNRoutes failed: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes from the good namespace, got %d: %+v", len(routes), routes)
	}
	goodStealth := turn.StealthHostForNamespace("good", base)
	found := false
	for _, r := range routes {
		if r.Match == goodStealth {
			found = true
		}
	}
	if !found {
		t.Errorf("good namespace stealth route missing despite malformed sibling")
	}
}

// TestTURNDiscoveryConfig_Validate covers the required-field validation.
func TestTURNDiscoveryConfig_Validate(t *testing.T) {
	if errs := (&TURNDiscoveryConfig{NamespacesDir: "/x", BaseDomain: "example.com"}).Validate(); len(errs) != 0 {
		t.Errorf("valid config reported errors: %v", errs)
	}
	if errs := (&TURNDiscoveryConfig{BaseDomain: "example.com"}).Validate(); len(errs) == 0 {
		t.Errorf("missing namespaces_dir should be invalid")
	}
	if errs := (&TURNDiscoveryConfig{NamespacesDir: "/x"}).Validate(); len(errs) == 0 {
		t.Errorf("missing base_domain should be invalid")
	}
}
