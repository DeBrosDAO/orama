package sniproxy

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/turn"
)

// TestTURNRouteDiscoverer_staticRouteWinsMerge verifies that when a discovered
// stealth route collides with a static config route on the same SNI, the static
// route's backend is the one that ends up in the router (static wins).
func TestTURNRouteDiscoverer_staticRouteWinsMerge(t *testing.T) {
	dir := t.TempDir()
	const base = "example.com"
	writeTURNConfig(t, dir, "anchat", "node-1", "0.0.0.0:5349")

	stealthHost := turn.StealthHostForNamespace("anchat", base)
	fallback := Backend{Name: "caddy", Network: "tcp", Addr: "127.0.0.1:8443"}

	// Static config pins the very same stealth host to a DIFFERENT backend.
	static := func() ([]Route, Backend, error) {
		return []Route{
			{Match: stealthHost, Backend: Backend{Name: "static-override", Network: "tcp", Addr: "127.0.0.1:9999"}},
		}, fallback, nil
	}

	router := NewRouter(Backend{})
	d := NewTURNRouteDiscoverer(TURNDiscoveryConfig{NamespacesDir: dir, BaseDomain: base}, static, router, nil)
	if err := d.Apply(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Pick must return the static backend, not the discovered one.
	got := router.Pick(stealthHost)
	if got.Addr != "127.0.0.1:9999" {
		t.Errorf("static route should win: got backend %q, want 127.0.0.1:9999", got.Addr)
	}

	// The non-conflicting discovered alias must still be present.
	alias := router.Pick("turn.ns-anchat." + base)
	if alias.Addr != "127.0.0.1:5349" {
		t.Errorf("discovered alias route missing/wrong: got %q", alias.Addr)
	}

	// Fallback preserved from static source.
	if router.Fallback().Addr != "127.0.0.1:8443" {
		t.Errorf("fallback not preserved: got %q", router.Fallback().Addr)
	}
}

// TestTURNRouteDiscoverer_transientErrorKeepsPreviousRoutes verifies that once
// routes are installed, a subsequent Apply whose scan fails (namespaces dir
// removed) returns an error and leaves the previously-installed routes intact —
// a transient filesystem error must never blackhole :443.
func TestTURNRouteDiscoverer_transientErrorKeepsPreviousRoutes(t *testing.T) {
	parent := t.TempDir()
	nsDir := filepath.Join(parent, "namespaces")
	const base = "example.com"
	writeTURNConfig(t, nsDir, "anchat", "node-1", "0.0.0.0:5349")

	fallback := Backend{Name: "caddy", Network: "tcp", Addr: "127.0.0.1:8443"}
	static := func() ([]Route, Backend, error) { return nil, fallback, nil }

	router := NewRouter(Backend{})
	d := NewTURNRouteDiscoverer(TURNDiscoveryConfig{NamespacesDir: nsDir, BaseDomain: base}, static, router, nil)

	// First Apply succeeds and installs the anchat routes.
	if err := d.Apply(); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}
	before := len(router.Routes())
	if before != 2 {
		t.Fatalf("expected 2 routes after first apply, got %d", before)
	}

	// Make the namespaces dir unreadable by pointing the discoverer at a now-
	// removed path (simulate transient read failure).
	d.cfg.NamespacesDir = filepath.Join(parent, "gone")

	err := d.Apply()
	if err == nil {
		t.Fatalf("expected Apply to error on missing namespaces dir")
	}

	// Routes must be unchanged — the failed scan kept the previous table.
	after := router.Routes()
	if len(after) != before {
		t.Errorf("routes changed on transient error: had %d, now %d", before, len(after))
	}
	stealthHost := turn.StealthHostForNamespace("anchat", base)
	if router.Pick(stealthHost).Addr != "127.0.0.1:5349" {
		t.Errorf("previously-installed stealth route lost after transient error")
	}
}

// TestTURNRouteDiscoverer_staticSourceErrorKeepsRoutes verifies a failing static
// source (e.g. a bad config-file edit) also leaves the router untouched.
func TestTURNRouteDiscoverer_staticSourceErrorKeepsRoutes(t *testing.T) {
	dir := t.TempDir()
	const base = "example.com"
	writeTURNConfig(t, dir, "anchat", "node-1", "0.0.0.0:5349")

	fallback := Backend{Name: "caddy", Network: "tcp", Addr: "127.0.0.1:8443"}
	good := func() ([]Route, Backend, error) { return nil, fallback, nil }

	router := NewRouter(Backend{})
	d := NewTURNRouteDiscoverer(TURNDiscoveryConfig{NamespacesDir: dir, BaseDomain: base}, good, router, nil)
	if err := d.Apply(); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}
	before := len(router.Routes())

	// Swap in a static source that errors (simulates a malformed config file).
	d.static = func() ([]Route, Backend, error) { return nil, Backend{}, errors.New("bad config") }
	if err := d.Apply(); err == nil {
		t.Fatalf("expected Apply to error on static source failure")
	}
	if len(router.Routes()) != before {
		t.Errorf("routes changed on static-source error: had %d, now %d", before, len(router.Routes()))
	}
}

// TestMergeRoutes_staticPrecedesDiscovered checks first-match ordering: static
// routes precede discovered ones in the merged slice.
func TestMergeRoutes_staticPrecedesDiscovered(t *testing.T) {
	static := []Route{{Match: "a.example.com", Backend: Backend{Addr: "127.0.0.1:1"}}}
	discovered := []Route{
		{Match: "a.example.com", Backend: Backend{Addr: "127.0.0.1:2"}}, // conflict, dropped
		{Match: "b.example.com", Backend: Backend{Addr: "127.0.0.1:3"}},
	}
	merged := mergeRoutes(static, discovered)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged routes (1 static + 1 non-conflicting), got %d: %+v", len(merged), merged)
	}
	if merged[0].Match != "a.example.com" || merged[0].Backend.Addr != "127.0.0.1:1" {
		t.Errorf("static route should be first and unchanged: %+v", merged[0])
	}
	if merged[1].Match != "b.example.com" {
		t.Errorf("non-conflicting discovered route missing: %+v", merged)
	}
}
