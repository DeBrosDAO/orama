package namespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

// Bugboard #846, carried forward to the shared host TURN server (#283 part 2).
//
// The boot-time TURN restore used to pass an empty PublicIP, producing a config
// that crash-loops on "turn.public_ip: must not be empty". A crash-looped TURN
// gives zero ICE relay candidates AND never reaches the firewall rules, so the
// relay ports stay closed — an outage that looks like nothing at all.
//
// The guard moved when TURN went host-level: the config is now written by
// writeHostTURNConfig rather than a per-namespace spawn, so the refusal has to
// live there. These pin it in its new home.

// hostTURNCM builds a ClusterManager whose public-IP lookup yields ip.
func hostTURNCM(t *testing.T, ip string) *ClusterManager {
	t.Helper()
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "dns_nodes") && ip != "" {
			appendToSlice(dest, map[string]any{"IP": ip})
		}
		return nil
	}
	return &ClusterManager{
		db:          db,
		logger:      zap.NewNop(),
		localNodeID: "node-1",
		baseDomain:  "orama-devnet.network",
	}
}

func oneTenant() []hostTURNTenant {
	return []hostTURNTenant{{
		tenant:     turn.TenantConfig{Namespace: "anchat-v2", AuthSecret: "secret-v2"},
		listenPort: 3478,
		tlsPort:    0, // no TURNS: keeps the test off the cert filesystem
	}}
}

func TestWriteHostTURNConfig_refusesEmptyPublicIP(t *testing.T) {
	cm := hostTURNCM(t, "") // lookup yields nothing

	_, err := cm.writeHostTURNConfig(context.Background(), oneTenant())
	if err == nil {
		t.Fatal("wrote a TURN config with no public IP — the server crash-loops on it and the relay ports stay firewalled")
	}
	if !strings.Contains(err.Error(), "public IP unresolved") {
		t.Errorf("expected a clear unresolved-public-IP error, got: %v", err)
	}
}

// The config that IS written must be one the TURN server accepts, or the shared
// server crash-loops and takes every tenant on the host down at once.
func TestWriteHostTURNConfig_writesAValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "turn.yaml")
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	cm := hostTURNCM(t, "203.0.113.7")

	changed, err := cm.writeHostTURNConfig(context.Background(), oneTenant())
	if err != nil {
		t.Fatalf("writeHostTURNConfig: %v", err)
	}
	if !changed {
		t.Error("first write reported no change")
	}

	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read written config: %v", rerr)
	}
	cfg, perr := turn.ParseConfig(data)
	if perr != nil {
		t.Fatalf("the config we wrote does not parse — the TURN binary would refuse to start: %v", perr)
	}
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("the config we wrote is invalid — the shared server would crash-loop, taking every tenant down: %v", errs)
	}
	if secret, ok := cfg.TenantSecret("anchat-v2"); !ok || secret != "secret-v2" {
		t.Errorf("tenant did not survive the write/parse round trip: secret=%q ok=%v", secret, ok)
	}
}

// An unchanged tenant set must not rewrite the file. The server polls it, so a
// gratuitous rewrite on every 60s sweep is churn that looks like a real change.
func TestWriteHostTURNConfig_unchangedSetIsNotRewritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	cm := hostTURNCM(t, "203.0.113.7")

	if _, err := cm.writeHostTURNConfig(context.Background(), oneTenant()); err != nil {
		t.Fatalf("first write: %v", err)
	}
	changed, err := cm.writeHostTURNConfig(context.Background(), oneTenant())
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Error("an identical tenant set reported a change — the sweep would log a tenant update every tick")
	}
}

// Two namespaces on one host must land in ONE config, each with its own secret.
// This is the whole point of #283: the second namespace on a host used to get no
// relay at all.
func TestWriteHostTURNConfig_multipleTenantsShareOneServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	cm := hostTURNCM(t, "203.0.113.7")
	// Two namespaces on one host, as the allocator produces them: the same
	// well-known listener ports. Their relay blocks DIFFER in the database
	// (AllocateTURNPorts hands out the next free block per host), but that
	// difference is no longer expressible here — hostTURNTenant carries no relay
	// fields, because one process has one relay range. An earlier version of this
	// change did carry them and refused any set whose members disagreed, which
	// rejected every real multi-tenant host. The illegal state is now
	// unrepresentable; the host-wide range is asserted separately below.
	tenants := []hostTURNTenant{
		{tenant: turn.TenantConfig{Namespace: "anchat-test", AuthSecret: "secret-test"}, listenPort: 3478},
		{tenant: turn.TenantConfig{Namespace: "anchat-v2", AuthSecret: "secret-v2"}, listenPort: 3478},
	}

	if _, err := cm.writeHostTURNConfig(context.Background(), tenants); err != nil {
		t.Fatalf("writeHostTURNConfig: %v", err)
	}
	data, _ := os.ReadFile(path)
	cfg, perr := turn.ParseConfig(data)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	for ns, want := range map[string]string{"anchat-test": "secret-test", "anchat-v2": "secret-v2"} {
		got, ok := cfg.TenantSecret(ns)
		if !ok {
			t.Errorf("namespace %s is not served — this is the #283 ceiling, unfixed", ns)
			continue
		}
		if got != want {
			t.Errorf("namespace %s resolved to %q, want %q — tenants must never share a secret", ns, got, want)
		}
	}
}

// Listener settings are host-wide. If two namespaces on one node disagree, the
// allocator produced something a single shared server cannot honour: say so
// rather than silently picking one and mis-serving the other.
func TestWriteHostTURNConfig_refusesDisagreeingListenerSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	cm := hostTURNCM(t, "203.0.113.7")
	tenants := []hostTURNTenant{
		{tenant: turn.TenantConfig{Namespace: "a", AuthSecret: "s1"}, listenPort: 3478},
		{tenant: turn.TenantConfig{Namespace: "b", AuthSecret: "s2"}, listenPort: 3999},
	}

	if _, err := cm.writeHostTURNConfig(context.Background(), tenants); err == nil {
		t.Error("accepted tenants that disagree on listener settings")
	}
}

// swapHostTURNConfigPath redirects the shared TURN config into a test-local
// path and returns a restore func.
func swapHostTURNConfigPath(t *testing.T, path string) func() {
	t.Helper()
	prev := hostTURNConfigPath
	hostTURNConfigPath = path
	return func() { hostTURNConfigPath = prev }
}

// Bugboard #158, carried forward. If the TURN server keeps a stale auth secret
// after the namespace's DB secret rotates, the gateway mints credentials TURN
// rejects and every call fails auth (Allocate 400 → zero relay candidates).
//
// The old repair was ReconcileTURN patching the per-namespace config in place.
// The shared server has no per-namespace config to patch: the whole host config
// is rebuilt from the DB each sweep, so drift is repaired by construction. This
// pins that — it is the property, not the mechanism, that must survive.
func TestWriteHostTURNConfig_repairsASecretThatDriftedFromTheDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	cm := hostTURNCM(t, "203.0.113.7")

	stale := oneTenant()
	stale[0].tenant.AuthSecret = "old-rotated-out-secret"
	if _, err := cm.writeHostTURNConfig(context.Background(), stale); err != nil {
		t.Fatalf("seed stale config: %v", err)
	}

	// The DB now holds a different secret for the same namespace.
	changed, err := cm.writeHostTURNConfig(context.Background(), oneTenant())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !changed {
		t.Fatal("a rotated secret was not detected as a change — TURN would keep rejecting every credential the gateway mints")
	}

	data, _ := os.ReadFile(path)
	cfg, perr := turn.ParseConfig(data)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	got, ok := cfg.TenantSecret("anchat-v2")
	if !ok || got != "secret-v2" {
		t.Errorf("secret not repaired: got %q ok=%v, want %q", got, ok, "secret-v2")
	}
}

// The shared server must relay from the HOST-WIDE range, not any one tenant's
// 800-port block.
//
// This is the defect the first version of this change shipped with. The relay
// range was taken from tenants[0] and the writer refused any set whose members
// disagreed — but AllocateTURNPorts hands each namespace on a host the NEXT FREE
// block (49152-49951, then 49952-50751, …), so two tenants on one host ALWAYS
// disagree. Every multi-tenant host would have failed to write a config at all:
// the exact scenario #283 exists to fix, failing with a new log line.
//
// The range must also match what the root firewall phase opens, or the server
// relays on ports UFW drops.
func TestWriteHostTURNConfig_usesTheHostWideRelayRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	cm := hostTURNCM(t, "203.0.113.7")
	tenants := []hostTURNTenant{
		{tenant: turn.TenantConfig{Namespace: "anchat-test", AuthSecret: "s1"}, listenPort: 3478},
		{tenant: turn.TenantConfig{Namespace: "anchat-v2", AuthSecret: "s2"}, listenPort: 3478},
	}

	if _, err := cm.writeHostTURNConfig(context.Background(), tenants); err != nil {
		t.Fatalf("refused to write a config for two tenants on one host: %v", err)
	}

	data, _ := os.ReadFile(path)
	cfg, perr := turn.ParseConfig(data)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	if cfg.RelayPortStart != TURNRelayPortRangeStart || cfg.RelayPortEnd != TURNRelayPortRangeEnd {
		t.Errorf("relay range = %d-%d, want the host-wide %d-%d; a per-tenant block would confine every other tenant's relays",
			cfg.RelayPortStart, cfg.RelayPortEnd, TURNRelayPortRangeStart, TURNRelayPortRangeEnd)
	}
}

// The legacy per-namespace TURN configs were mode 0644 and hold that namespace's
// HMAC secret, so any local user could read one and mint valid TURN credentials.
// Retiring the unit without deleting its config would leave exactly the exposure
// the shared 0600 config exists to close — on precisely the hosts this migration
// touches.
func TestRemoveLegacyTURNConfig_deletesTheSecretFile(t *testing.T) {
	nsDir := t.TempDir()
	cfgDir := filepath.Join(nsDir, "configs")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := filepath.Join(cfgDir, "turn-node-1.yaml")
	if err := os.WriteFile(legacy, []byte("auth_secret: the-namespace-hmac-secret\n"), 0644); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}

	cm := hostTURNCM(t, "203.0.113.7")
	cm.removeLegacyTURNConfig("anchat-v2", nsDir)

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("the legacy 0644 TURN config is still on disk — its HMAC secret stays readable by any local user")
	}
}

// Idempotent: this runs once a minute per namespace forever, including on nodes
// that never had a per-namespace TURN.
func TestRemoveLegacyTURNConfig_missingFileIsNotAnError(t *testing.T) {
	cm := hostTURNCM(t, "203.0.113.7")
	cm.removeLegacyTURNConfig("anchat-v2", t.TempDir()) // must not panic
}

// The delete path must come from the directory actually walked, never from a
// namespace name — namespace names are not validated on the creation path, and
// this is an unlink.
func TestRemoveLegacyTURNConfig_confinedToTheGivenDirectory(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.yaml")
	if err := os.WriteFile(outside, []byte("do not delete"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	nsDir := filepath.Join(root, "ns")
	if err := os.MkdirAll(filepath.Join(nsDir, "configs"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cm := hostTURNCM(t, "203.0.113.7")
	cm.removeLegacyTURNConfig("../../outside", nsDir)

	if _, err := os.Stat(outside); err != nil {
		t.Error("a file outside the namespace directory was deleted — the namespace name must not steer the path")
	}
}

// The migration is not complete until turn.env is gone: both the upgrade's
// rolling restart and hostRunsTURN() enumerate namespaces by that file, so
// leaving it makes the upgrade restart a unit whose config was just deleted —
// a permanent crash-loop.
func TestRemoveLegacyTURNConfig_alsoRemovesTheEnvFile(t *testing.T) {
	nsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(nsDir, "configs"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envFile := filepath.Join(nsDir, "turn.env")
	if err := os.WriteFile(envFile, []byte("TURN_CONFIG=/x.yaml\n"), 0644); err != nil {
		t.Fatalf("seed env: %v", err)
	}

	cm := hostTURNCM(t, "203.0.113.7")
	cm.removeLegacyTURNConfig("anchat-v2", nsDir)

	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Error("turn.env survived — the upgrade will keep restarting a configless TURN unit into a crash-loop")
	}
}
