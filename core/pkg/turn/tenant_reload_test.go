package turn

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Bugboard #283 part 2. One shared TURN server per host serves every namespace
// on it, so the tenant set changes whenever ANY namespace enables or disables
// WebRTC. Restarting the process to pick that up would drop every OTHER tenant's
// active relays — turning a routine per-namespace operation into a host-wide
// call drop. The tenant set is therefore reloaded from the config file in place.
//
// These tests exist to hold that property and the isolation boundary around it:
// a reload must never authorize a namespace the file does not list, and a bad or
// unreadable file must never revoke tenants that are currently working.

// reloadServer builds a Server with no listeners — enough to exercise the tenant
// machinery without binding 3478.
func reloadServer(t *testing.T, cfg *Config) *Server {
	t.Helper()
	s := &Server{config: cfg, logger: zap.NewNop()}
	set, pending, err := s.buildTenantSet(cfg, nil)
	if err != nil {
		t.Fatalf("buildTenantSet: %v", err)
	}
	s.tenants = set
	startPendingWatchers(pending)
	t.Cleanup(s.stopAllStealthWatchers)
	return s
}

// writeTestCertPair generates a self-signed cert/key pair for a stealth host.
func writeTestCertPair(t *testing.T, dir, host string) (string, string) {
	t.Helper()
	certPath := filepath.Join(dir, host+".crt")
	keyPath := filepath.Join(dir, host+".key")
	if err := GenerateSelfSignedCert(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatalf("GenerateSelfSignedCert: %v", err)
	}
	return certPath, keyPath
}

// writeCfg marshals cfg to path the same way the reconciler does.
func writeCfg(t *testing.T, path string, cfg Config) {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func baseCfg(tenants ...TenantConfig) Config {
	return Config{
		ListenAddr:     "0.0.0.0:3478",
		PublicIP:       "203.0.113.1",
		Realm:          "orama-devnet.network",
		RelayPortStart: 49152,
		RelayPortEnd:   49951,
		Tenants:        tenants,
	}
}

// The core of the fix: a namespace added to the file starts authenticating
// without the process restarting.
func TestReloadTenants_addsANamespaceWithoutRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	writeCfg(t, path, start)

	s := reloadServer(t, &start)
	if _, ok := s.tenantSecret("anchat-v2"); ok {
		t.Fatal("anchat-v2 authorized before it was configured")
	}

	writeCfg(t, path, baseCfg(
		TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"},
		TenantConfig{Namespace: "anchat-v2", AuthSecret: "secret-v2"},
	))
	if err := s.reloadTenants(path); err != nil {
		t.Fatalf("reloadTenants: %v", err)
	}

	got, ok := s.tenantSecret("anchat-v2")
	if !ok {
		t.Error("the added namespace is still not served — this is the #283 ceiling, unfixed")
	}
	if got != "secret-v2" {
		t.Errorf("added tenant resolved to %q, want %q", got, "secret-v2")
	}
	// The tenant that was already there must be untouched.
	if s0, ok := s.tenantSecret("anchat-test"); !ok || s0 != "secret-test" {
		t.Errorf("the existing tenant was disturbed by adding another: secret=%q ok=%v", s0, ok)
	}
}

// Removing a namespace must actually revoke it — a stale tenant would keep
// relaying for a namespace that no longer pays for it.
func TestReloadTenants_removesANamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(
		TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"},
		TenantConfig{Namespace: "anchat-v2", AuthSecret: "secret-v2"},
	)
	writeCfg(t, path, start)
	s := reloadServer(t, &start)

	writeCfg(t, path, baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"}))
	if err := s.reloadTenants(path); err != nil {
		t.Fatalf("reloadTenants: %v", err)
	}

	if _, ok := s.tenantSecret("anchat-v2"); ok {
		t.Error("a removed namespace is still authorized")
	}
	if _, ok := s.tenantSecret("anchat-test"); !ok {
		t.Error("removing one tenant revoked another")
	}
}

// A half-written or unreadable config must NOT revoke working tenants. The
// reconciler writes atomically, but a disk error or a truncated read must fail
// closed toward "keep serving", not toward "serve nobody".
func TestReloadTenants_badConfigKeepsTheCurrentTenants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)

	if err := os.WriteFile(path, []byte("this: is: not: valid: yaml\n\t\tbroken"), 0600); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	if err := s.reloadTenants(path); err == nil {
		t.Error("a broken config parsed successfully")
	}

	if secret, ok := s.tenantSecret("anchat-test"); !ok || secret != "secret-test" {
		t.Error("a broken config revoked a working tenant — every live relay on the host would stop authenticating")
	}
}

// Same for a config that vanishes.
func TestReloadTenants_missingFileKeepsTheCurrentTenants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := s.reloadTenants(path); err == nil {
		t.Error("a missing config reloaded successfully")
	}
	if _, ok := s.tenantSecret("anchat-test"); !ok {
		t.Error("a missing config revoked a working tenant")
	}
}

// An empty tenant list must be rejected rather than swapped in: it would
// silently stop authenticating everyone on the host.
func TestReloadTenants_emptyTenantListIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)

	writeCfg(t, path, baseCfg())
	if err := s.reloadTenants(path); err == nil {
		t.Error("an empty tenant list was accepted")
	}
	if _, ok := s.tenantSecret("anchat-test"); !ok {
		t.Error("an empty tenant list revoked every working tenant")
	}
}

// A tenant with no secret cannot authenticate anyone; accepting it would swap in
// a set where that namespace silently fails. Reject the whole reload instead.
func TestReloadTenants_tenantWithoutSecretIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)

	writeCfg(t, path, baseCfg(
		TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"},
		TenantConfig{Namespace: "broken", AuthSecret: ""},
	))
	if err := s.reloadTenants(path); err == nil {
		t.Error("a tenant with no auth secret was accepted")
	}
}

// The isolation boundary must survive a reload: after adding a tenant, one
// tenant's credential must still not validate under another's secret.
func TestReloadTenants_tenantsStayIsolatedAfterReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)

	writeCfg(t, path, baseCfg(
		TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"},
		TenantConfig{Namespace: "anchat-v2", AuthSecret: "secret-v2"},
	))
	if err := s.reloadTenants(path); err != nil {
		t.Fatalf("reloadTenants: %v", err)
	}

	a, _ := s.tenantSecret("anchat-test")
	b, _ := s.tenantSecret("anchat-v2")
	if a == b {
		t.Fatal("two tenants resolved to the same secret after a reload")
	}

	username := "9999999999:anchat-v2"
	password := GeneratePassword(b, username)
	if !ValidateCredentials(b, username, password, "anchat-v2") {
		t.Error("a tenant's own credential stopped validating after a reload")
	}
	if ValidateCredentials(a, username, password, "anchat-v2") {
		t.Error("a credential validated under another tenant's secret after a reload")
	}
}

// A namespace absent from the file must never be authorized, however the file
// got that way.
func TestTenantSecret_unservedNamespaceIsNeverAuthorized(t *testing.T) {
	cfg := baseCfg(TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"})
	s := reloadServer(t, &cfg)

	if secret, ok := s.tenantSecret("someone-elses-namespace"); ok {
		t.Errorf("an unserved namespace was authorized (secret=%q) — this is the cross-tenant relay hole", secret)
	}
}

// The legacy single-tenant config must keep working: nodes upgrade one at a
// time, and a node still on the old per-namespace config must not lose TURN.
func TestBuildTenantSet_acceptsLegacySingleTenantConfig(t *testing.T) {
	cfg := Config{
		ListenAddr:     "0.0.0.0:3478",
		PublicIP:       "203.0.113.1",
		Realm:          "orama-devnet.network",
		RelayPortStart: 49152,
		RelayPortEnd:   49951,
		Namespace:      "anchat-test",
		AuthSecret:     "legacy-secret",
	}
	s := reloadServer(t, &cfg)

	if secret, ok := s.tenantSecret("anchat-test"); !ok || secret != "legacy-secret" {
		t.Errorf("legacy single-tenant config lost its tenant: secret=%q ok=%v", secret, ok)
	}
	if _, ok := s.tenantSecret("anchat-v2"); ok {
		t.Error("a legacy single-tenant config authorized another namespace")
	}
}

// The reconciler writes yaml.Marshal(Config); the server parses it back. Strict
// decoding rejects unknown keys, so this round trip is a hard contract — it is
// what broke when `tenants` was added to Config but not to the old mirror
// struct in cmd/turn.
func TestParseConfig_roundTripsTheReconcilerOutput(t *testing.T) {
	cfg := baseCfg(
		TenantConfig{Namespace: "a", AuthSecret: "s1"},
		TenantConfig{
			Namespace: "b", AuthSecret: "s2",
			StealthDomain:      "cdn-abc.orama-devnet.network",
			TLSStealthCertPath: "/tmp/c.pem",
			TLSStealthKeyPath:  "/tmp/k.pem",
		},
	)
	cfg.TURNSListenAddr = "0.0.0.0:5349"
	cfg.TLSCertPath = "/tmp/wc.pem"
	cfg.TLSKeyPath = "/tmp/wk.pem"

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, perr := ParseConfig(data)
	if perr != nil {
		t.Fatalf("the config the reconciler writes does not parse — the TURN binary would refuse to start: %v", perr)
	}
	if len(got.Tenants) != 2 {
		t.Fatalf("got %d tenants after the round trip, want 2", len(got.Tenants))
	}
	if got.Tenants[1].StealthDomain != "cdn-abc.orama-devnet.network" {
		t.Errorf("stealth domain lost in the round trip: %+v", got.Tenants[1])
	}
	if errs := got.Validate(); len(errs) != 0 {
		t.Errorf("round-tripped config is invalid: %v", errs)
	}
}

// A tenant that leaves the set must take its stealth cert watcher with it.
// Without this every stealth enable/disable — and every cert-path change — leaks
// a goroutine polling a file nothing serves, for the life of the node.
func TestReloadTenants_stopsWatchersForDroppedStealthCerts(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertPair(t, dir, "cdn-a.orama-devnet.network")

	path := filepath.Join(dir, "turn.yaml")
	withStealth := baseCfg(TenantConfig{
		Namespace: "a", AuthSecret: "s1",
		StealthDomain:      "cdn-a.orama-devnet.network",
		TLSStealthCertPath: certPath,
		TLSStealthKeyPath:  keyPath,
	})
	writeCfg(t, path, withStealth)
	s := reloadServer(t, &withStealth)

	sc, ok := s.currentTenants().byHost["cdn-a.orama-devnet.network"]
	if !ok || sc == nil {
		t.Fatal("stealth cert was not loaded")
	}

	// Drop stealth for that tenant.
	writeCfg(t, path, baseCfg(TenantConfig{Namespace: "a", AuthSecret: "s1"}))
	if err := s.reloadTenants(path); err != nil {
		t.Fatalf("reloadTenants: %v", err)
	}

	select {
	case <-sc.stop:
		// stopped, as required
	default:
		t.Error("the dropped tenant's cert watcher is still running — one goroutine leaks per stealth disable")
	}
	if _, still := s.stealthCertFor("cdn-a.orama-devnet.network"); still {
		t.Error("a dropped stealth hostname is still served a certificate")
	}
}

// An unchanged stealth cert must keep its existing watcher rather than being
// reloaded from disk on every 15s tick.
func TestReloadTenants_keepsWatcherForUnchangedStealthCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertPair(t, dir, "cdn-a.orama-devnet.network")

	path := filepath.Join(dir, "turn.yaml")
	tenant := TenantConfig{
		Namespace: "a", AuthSecret: "s1",
		StealthDomain:      "cdn-a.orama-devnet.network",
		TLSStealthCertPath: certPath,
		TLSStealthKeyPath:  keyPath,
	}
	cfg := baseCfg(tenant)
	writeCfg(t, path, cfg)
	s := reloadServer(t, &cfg)
	before := s.currentTenants().byHost["cdn-a.orama-devnet.network"]

	// Add an unrelated tenant; the stealth one is untouched.
	writeCfg(t, path, baseCfg(tenant, TenantConfig{Namespace: "b", AuthSecret: "s2"}))
	if err := s.reloadTenants(path); err != nil {
		t.Fatalf("reloadTenants: %v", err)
	}

	after := s.currentTenants().byHost["cdn-a.orama-devnet.network"]
	if before != after {
		t.Error("an unchanged stealth cert was rebuilt — a transient read error would take a working tenant's stealth endpoint down")
	}
	select {
	case <-before.stop:
		t.Error("an unchanged tenant's cert watcher was stopped")
	default:
	}
}
